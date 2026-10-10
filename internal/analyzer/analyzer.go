// Package analyzer parses pprof profiles and produces top-function
// reports, diffs, heuristic findings and flamegraph data.
package analyzer

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/google/pprof/profile"
)

// FuncStat is one function's aggregated samples.
type FuncStat struct {
	Function string  `json:"function"`
	FileLine string  `json:"file_line,omitempty"`
	Flat     int64   `json:"flat"`
	FlatPct  float64 `json:"flat_pct"`
	Cum      int64   `json:"cum"`
	CumPct   float64 `json:"cum_pct"`
}

// Report is the analysis of one profile.
type Report struct {
	ProfileID  string     `json:"profile_id"`
	Total      int64      `json:"total_samples"`
	SampleType string     `json:"sample_type"`
	Units      string     `json:"units"`
	TopFlat    []FuncStat `json:"top_flat"`
	TopCum     []FuncStat `json:"top_cum"`
	Findings   []Finding  `json:"findings"`
	Heuristic  bool       `json:"heuristic"`
}

// Finding is one heuristic observation.
type Finding struct {
	Kind      string `json:"kind"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Heuristic bool   `json:"heuristic"`
}

// Parse parses raw pprof protobuf bytes.
func Parse(data []byte) (*profile.Profile, error) {
	p, err := profile.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse pprof: %w", err)
	}
	if err := p.CheckValid(); err != nil {
		return nil, fmt.Errorf("invalid pprof: %w", err)
	}
	return p, nil
}

// Analyze aggregates flat/cum per function and returns top_n entries.
func Analyze(profileID string, p *profile.Profile, topN int) (*Report, error) {
	if topN <= 0 {
		topN = 15
	}
	flat := map[string]*FuncStat{}
	cum := map[string]*FuncStat{}
	var total int64
	sampleType := ""
	units := ""
	if len(p.SampleType) > 0 {
		sampleType = p.SampleType[0].Type
		units = p.SampleType[0].Unit
	}
	for _, s := range p.Sample {
		var v int64
		if len(s.Value) > 0 {
			v = s.Value[0]
		}
		total += v
		seen := map[string]bool{}
		for i, loc := range s.Location {
			for _, line := range loc.Line {
				fn := line.Function
				if fn == nil {
					continue
				}
				name := fn.Name
				key := name
				st := cum[key]
				if st == nil {
					st = &FuncStat{Function: name, FileLine: fileLine(fn)}
					cum[key] = st
				}
				if !seen[key] {
					st.Cum += v
					seen[key] = true
				}
				if i == 0 {
					fst := flat[key]
					if fst == nil {
						fst = &FuncStat{Function: name, FileLine: fileLine(fn)}
						flat[key] = fst
					}
					fst.Flat += v
					fst.Cum += v
				}
			}
		}
	}
	// Merge cum into flat entries for display.
	for k, c := range cum {
		if f, ok := flat[k]; ok {
			f.Cum = c.Cum
		} else {
			flat[k] = &FuncStat{Function: c.Function, FileLine: c.FileLine, Cum: c.Cum}
		}
	}
	flatList := valuesSorted(flat, true, total)
	cumList := valuesSorted(cum, false, total)
	rep := &Report{ProfileID: profileID, Total: total, SampleType: sampleType, Units: units, Heuristic: true}
	rep.TopFlat = take(flatList, topN)
	rep.TopCum = take(cumList, topN)
	rep.Findings = gcFindings(p, rep)
	return rep, nil
}

func fileLine(fn *profile.Function) string {
	if fn.Filename == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", fn.Filename, 0)
}

func valuesSorted(m map[string]*FuncStat, byFlat bool, total int64) []FuncStat {
	out := make([]FuncStat, 0, len(m))
	for _, v := range m {
		if total > 0 {
			v.FlatPct = 100 * float64(v.Flat) / float64(total)
			v.CumPct = 100 * float64(v.Cum) / float64(total)
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if byFlat {
			if out[i].Flat != out[j].Flat {
				return out[i].Flat > out[j].Flat
			}
			return out[i].Function < out[j].Function
		}
		if out[i].Cum != out[j].Cum {
			return out[i].Cum > out[j].Cum
		}
		return out[i].Function < out[j].Function
	})
	return out
}

func take(in []FuncStat, n int) []FuncStat {
	if len(in) > n {
		return in[:n]
	}
	return in
}

// gcFindings applies cheap allocation-churn heuristics.
func gcFindings(p *profile.Profile, rep *Report) []Finding {
	var out []Finding
	if len(p.SampleType) == 0 {
		return out
	}
	st := p.SampleType[0].Type
	if st == "alloc_space" || st == "inuse_space" {
		for _, f := range rep.TopFlat {
			base := f.Function
			if strings.Contains(base, "bytes.Buffer") || strings.Contains(base, "append") || strings.Contains(base, "makeslice") {
				out = append(out, Finding{Kind: "gc_pressure", Severity: "medium",
					Message:   fmt.Sprintf("top allocator %s holds %.1f%% — consider sync.Pool or pre-sized buffers in the hot path", f.Function, f.FlatPct),
					Heuristic: true})
				break
			}
		}
	}
	return out
}

// DiffEntry is one function's change between two profiles.
type DiffEntry struct {
	Function  string  `json:"function"`
	BaseFlat  int64   `json:"base_flat"`
	CurFlat   int64   `json:"current_flat"`
	DeltaFlat int64   `json:"delta_flat"`
	DeltaPct  float64 `json:"delta_flat_pct_points"`
}

// Diff compares base vs current profiles of the same type.
func Diff(base, cur *profile.Profile) (regressions, improvements []DiffEntry, baseTotal, curTotal int64) {
	bf := flatMap(base)
	cf := flatMap(cur)
	for _, s := range base.Sample {
		if len(s.Value) > 0 {
			baseTotal += s.Value[0]
		}
	}
	for _, s := range cur.Sample {
		if len(s.Value) > 0 {
			curTotal += s.Value[0]
		}
	}
	keys := map[string]bool{}
	for k := range bf {
		keys[k] = true
	}
	for k := range cf {
		keys[k] = true
	}
	for k := range keys {
		b, c := bf[k], cf[k]
		var bp, cp float64
		if baseTotal > 0 {
			bp = 100 * float64(b) / float64(baseTotal)
		}
		if curTotal > 0 {
			cp = 100 * float64(c) / float64(curTotal)
		}
		e := DiffEntry{Function: k, BaseFlat: b, CurFlat: c, DeltaFlat: c - b, DeltaPct: cp - bp}
		if e.DeltaFlat > 0 {
			regressions = append(regressions, e)
		} else if e.DeltaFlat < 0 {
			improvements = append(improvements, e)
		}
	}
	sort.Slice(regressions, func(i, j int) bool { return regressions[i].DeltaFlat > regressions[j].DeltaFlat })
	sort.Slice(improvements, func(i, j int) bool { return improvements[i].DeltaFlat < improvements[j].DeltaFlat })
	return regressions, improvements, baseTotal, curTotal
}

func flatMap(p *profile.Profile) map[string]int64 {
	m := map[string]int64{}
	for _, s := range p.Sample {
		var v int64
		if len(s.Value) > 0 {
			v = s.Value[0]
		}
		if len(s.Location) == 0 {
			continue
		}
		for _, line := range s.Location[0].Line {
			if line.Function == nil {
				continue
			}
			m[line.Function.Name] += v
		}
	}
	return m
}

// Folded renders folded stacks: "a;b;c count" per line.
func Folded(p *profile.Profile) (string, int64) {
	counts := map[string]int64{}
	var total int64
	for _, s := range p.Sample {
		var v int64
		if len(s.Value) > 0 {
			v = s.Value[0]
		}
		total += v
		var frames []string
		for i := len(s.Location) - 1; i >= 0; i-- {
			loc := s.Location[i]
			name := "unknown"
			if len(loc.Line) > 0 && loc.Line[0].Function != nil {
				name = loc.Line[0].Function.Name
			}
			name = strings.ReplaceAll(name, ";", ":")
			frames = append(frames, name)
		}
		counts[strings.Join(frames, ";")] += v
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %d\n", k, counts[k])
	}
	return b.String(), total
}

// TreeNode is a flamegraph tree node.
type TreeNode struct {
	Name     string      `json:"name"`
	Value    int64       `json:"value"`
	Children []*TreeNode `json:"children,omitempty"`
}

// Tree aggregates samples into a prefix tree with width/depth caps.
func Tree(p *profile.Profile, maxDepth, maxWidth int) (*TreeNode, bool) {
	if maxDepth <= 0 {
		maxDepth = 200
	}
	if maxWidth <= 0 {
		maxWidth = 5000
	}
	root := &TreeNode{Name: "root"}
	truncated := false
	nodes := 1
	for _, s := range p.Sample {
		var v int64
		if len(s.Value) > 0 {
			v = s.Value[0]
		}
		root.Value += v
		cur := root
		depth := 0
		for i := len(s.Location) - 1; i >= 0 && depth < maxDepth; i-- {
			loc := s.Location[i]
			name := "unknown"
			if len(loc.Line) > 0 && loc.Line[0].Function != nil {
				name = loc.Line[0].Function.Name
			}
			var next *TreeNode
			for _, ch := range cur.Children {
				if ch.Name == name {
					next = ch
					break
				}
			}
			if next == nil {
				if nodes >= maxWidth {
					truncated = true
					break
				}
				next = &TreeNode{Name: name}
				cur.Children = append(cur.Children, next)
				nodes++
			}
			next.Value += v
			cur = next
			depth++
		}
		if depth >= maxDepth {
			truncated = true
		}
	}
	sortTree(root)
	return root, truncated
}

func sortTree(n *TreeNode) {
	sort.Slice(n.Children, func(i, j int) bool { return n.Children[i].Value > n.Children[j].Value })
	for _, ch := range n.Children {
		sortTree(ch)
	}
}

// GoroutineLeakCheck compares two goroutine counts with the dominant waiter.
func GoroutineLeakCheck(before, after int, dominantStack string) *Finding {
	if after > before && after-before >= 10 {
		return &Finding{Kind: "goroutine_leak_suspected", Severity: "high",
			Message:   fmt.Sprintf("goroutines grew %d → %d; dominant waiter %q — check for blocked chan/mutex holders", before, after, dominantStack),
			Heuristic: true}
	}
	return nil
}
