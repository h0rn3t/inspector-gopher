// Package dashboard renders self-contained HTML flamegraph reports.
//
// The generated pages embed their data and need no network access, so they
// render inside sandboxed MCP Apps iframes (SEP-1865), in a plain browser
// tab (Claude Code preview fallback), and from a file:// artifact alike.
package dashboard

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	json "encoding/json/v2"

	"github.com/h0rn3t/inspector-gopher/internal/analyzer"
	"github.com/h0rn3t/inspector-gopher/internal/store"
)

// MIMEApp is the SEP-1865 MIME type for HTML UI resources.
const MIMEApp = "text/html;profile=mcp-app"

// ResourceURI is the generic flamegraph viewer UI resource.
const ResourceURI = "ui://inspector-gopher/flamegraph"

// TemplatePrefix prefixes per-profile UI resources:
// ui://inspector-gopher/flamegraph/{profile_id}.
const TemplatePrefix = "ui://inspector-gopher/flamegraph/"

// URITemplate is the resource template for per-profile dashboards.
const URITemplate = "ui://inspector-gopher/flamegraph/{profile_id}"

// FilenamePrefix prefixes dashboard artifacts on disk.
const FilenamePrefix = "flamegraph-"

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,120}$`)

// ValidID reports whether id is safe to embed in file names, URIs and URLs.
func ValidID(id string) bool {
	return validID.MatchString(id)
}

// FuncRow is one top-function table row.
type FuncRow struct {
	Function string
	Flat     int64
	FlatPct  float64
	Cum      int64
	CumPct   float64
}

// Rows converts analyzer stats to render rows.
func Rows(stats []analyzer.FuncStat) []FuncRow {
	rows := make([]FuncRow, 0, len(stats))
	for _, st := range stats {
		rows = append(rows, FuncRow{
			Function: st.Function, Flat: st.Flat, FlatPct: st.FlatPct,
			Cum: st.Cum, CumPct: st.CumPct,
		})
	}
	return rows
}

// Data is everything Render needs for one profile dashboard.
type Data struct {
	ProfileID   string
	Title       string
	Target      string
	Kind        string
	SampleType  string
	Units       string
	CollectedAt string
	Total       int64
	Truncated   bool
	Top         []FuncRow
	Tree        *analyzer.TreeNode
	Folded      string
}

// IndexEntry is one row of the dashboards landing page.
type IndexEntry struct {
	ID         string
	Target     string
	Kind       string
	SampleType string
}

// Dir resolves the dashboard artifact directory: custom when set, otherwise
// ~/.cache/inspector-gopher/dashboards (os temp as a last resort).
func Dir(custom string) string {
	if strings.TrimSpace(custom) != "" {
		return custom
	}
	if base := store.DefaultCacheDir(); base != "" {
		return filepath.Join(base, "dashboards")
	}
	return filepath.Join(os.TempDir(), "inspector-gopher-dashboards")
}

// Save writes html to dir/flamegraph-<profileID>.html and returns its path.
func Save(dir, profileID, page string) (string, error) {
	if !ValidID(profileID) {
		return "", fmt.Errorf("invalid profile id %q", profileID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create dashboard dir: %w", err)
	}
	path := filepath.Join(dir, FilenamePrefix+profileID+".html")
	if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
		return "", fmt.Errorf("write dashboard: %w", err)
	}
	return path, nil
}

// Render builds the self-contained dashboard page. All interpolated text is
// HTML-escaped; the embedded tree JSON is encoded so it cannot break out of
// its <script> block.
func Render(d Data) (string, error) {
	tree := d.Tree
	if tree == nil {
		tree = &analyzer.TreeNode{Name: "root"}
	}
	treeJSON, err := json.Marshal(tree)
	if err != nil {
		return "", fmt.Errorf("encode flamegraph tree: %w", err)
	}
	// Keep the JSON inside <script>: escape the one sequence that closes it.
	safeTree := strings.ReplaceAll(string(treeJSON), "</", "<\\/")

	title := d.Title
	if title == "" {
		title = "Flamegraph " + d.ProfileID
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(` — inspector-gopher</title>
<style>
:root{color-scheme:light dark}
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;margin:0 auto;max-width:1200px;padding:16px 20px 48px;line-height:1.45}
header p{margin:4px 0;color:#555}
.meta{display:flex;flex-wrap:wrap;gap:8px 20px;margin:12px 0;font-size:14px}
.meta b{font-weight:600}
table.top{border-collapse:collapse;width:100%;font-size:14px;margin:8px 0 4px}
table.top th,table.top td{text-align:left;padding:4px 8px;border-bottom:1px solid #ddd;font-variant-numeric:tabular-nums}
table.top td.fn{font-family:ui-monospace,Menlo,Consolas,monospace;word-break:break-all}
.bar{height:8px;background:#4c8dff;border-radius:2px;min-width:2px}
.toolbar{display:flex;gap:8px;align-items:center;margin:12px 0;flex-wrap:wrap}
.toolbar input{font-size:14px;padding:6px 10px;min-width:260px}
.toolbar button{font-size:14px;padding:6px 12px;cursor:pointer}
.crumbs{font-size:13px;margin:4px 0 8px;word-break:break-all}
.crumbs a{cursor:pointer;text-decoration:underline}
#flame{border:1px solid #ccc;border-radius:4px;background:#fff}
#flame rect{cursor:pointer;stroke:#fff;stroke-width:.5}
#flame rect.match{stroke:#111;stroke-width:2}
#flame.dim rect:not(.match){opacity:.3}
#flame text{pointer-events:none;font-size:11px;font-family:ui-monospace,Menlo,Consolas,monospace;fill:#111}
details{margin-top:16px}
pre.folded{background:#f6f6f6;border:1px solid #ddd;border-radius:4px;padding:10px;overflow:auto;font-size:12px;max-height:320px}
footer{margin-top:24px;font-size:12px;color:#777}
</style>
</head>
<body>
<header>
<h1>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</h1>
<div class="meta">
<span>profile <b>`)
	b.WriteString(html.EscapeString(d.ProfileID))
	b.WriteString(`</b></span>
<span>target <b>`)
	b.WriteString(html.EscapeString(d.Target))
	b.WriteString(`</b></span>
<span>kind <b>`)
	b.WriteString(html.EscapeString(d.Kind))
	b.WriteString(`</b></span>
<span>sample <b>`)
	b.WriteString(html.EscapeString(d.SampleType))
	if d.Units != "" {
		b.WriteString(` (`)
		b.WriteString(html.EscapeString(d.Units))
		b.WriteString(`)`)
	}
	b.WriteString(`</b></span>
<span>total <b>`)
	fmt.Fprintf(&b, "%d", d.Total)
	b.WriteString(`</b></span>`)
	if d.CollectedAt != "" {
		b.WriteString("\n<span>collected <b>")
		b.WriteString(html.EscapeString(d.CollectedAt))
		b.WriteString(`</b></span>`)
	}
	if d.Truncated {
		b.WriteString("\n<span><b>tree truncated</b> (depth/nodes cap hit — zoom into the hot branch)</span>")
	}
	b.WriteString(`
</div>
</header>
<section>
<h2>Top functions</h2>`)
	if len(d.Top) == 0 {
		b.WriteString("\n<p>No samples in this profile.</p>")
	} else {
		b.WriteString("\n<table class=\"top\"><thead><tr><th>function</th><th>flat</th><th>flat %</th><th></th></tr></thead><tbody>")
		for _, row := range d.Top {
			width := row.FlatPct
			if width < 0 {
				width = 0
			}
			if width > 100 {
				width = 100
			}
			fmt.Fprintf(&b, "\n<tr><td class=\"fn\">%s</td><td>%d</td><td>%.1f%%</td><td style=\"width:25%%\"><div class=\"bar\" style=\"width:%.1f%%\"></div></td></tr>",
				html.EscapeString(row.Function), row.Flat, row.FlatPct, width)
		}
		b.WriteString("\n</tbody></table>")
	}
	b.WriteString(`
</section>
<section>
<h2>Flamegraph</h2>
<div class="toolbar">
<input id="q" type="search" placeholder="Search frames (e.g. regexp, GC, mutex)" aria-label="Search frames">
<button id="reset" type="button">Reset zoom</button>
<span id="stats"></span>
</div>
<div class="crumbs" id="crumbs"></div>
<svg id="flame" width="100%" role="img" aria-label="Flamegraph"></svg>
</section>
<details>
<summary>Folded stacks (copy into speedscope / inferno)</summary>
<pre class="folded">`)
	b.WriteString(html.EscapeString(d.Folded))
	b.WriteString(`</pre>
</details>
<footer>Generated by inspector-gopher — every frame is proportional to its sample count; click a frame to zoom.</footer>
<script>
window.__INSPECTOR_FLAMEGRAPH__ = {"profile_id":`)
	profileJSON, err := json.Marshal(d.ProfileID)
	if err != nil {
		return "", fmt.Errorf("encode profile id: %w", err)
	}
	b.Write(profileJSON)
	b.WriteString(`,"total":`)
	fmt.Fprintf(&b, "%d", d.Total)
	b.WriteString(`,"tree":`)
	b.WriteString(safeTree)
	b.WriteString(`};
(function(){
var root=window.__INSPECTOR_FLAMEGRAPH__.tree;
var total=window.__INSPECTOR_FLAMEGRAPH__.total||1;
var svg=document.getElementById("flame"),crumbs=document.getElementById("crumbs"),
    stats=document.getElementById("stats"),q=document.getElementById("q");
var ROW=22,W=1160,path=[];
function hash(s){var h=5381,i;for(i=0;i<s.length;i++){h=((h<<5)+h+s.charCodeAt(i))|0;}return h>>>0;}
function color(n){if(n==="root"||n==="...")return "#e2e2e2";return "hsl("+(hash(n)%360)+",42%,64%)";}
function at(p){var n=root,i;for(i=0;i<p.length;i++){n=(n.children||[])[p[i]];}return n;}
function names(){return path.map(function(i,depth){var n=root;for(var k=0;k<=depth;k++){n=(n.children||[])[path[k]];}return n.name;});}
function draw(){
  var node=at(path),items=[];
  (function walk(n,depth,x,pp){
    items.push({n:n,depth:depth,x:x,pp:pp});
    var kids=n.children||[],cx=x,den=n.value||1,i;
    for(i=0;i<kids.length;i++){var w=kids[i].value/den;walk(kids[i],depth+1,cx,pp.concat(i));cx+=w;}
  })(node,0,0,path.slice());
  var maxDepth=0,i;for(i=0;i<items.length;i++){if(items[i].depth>maxDepth)maxDepth=items[i].depth;}
  svg.setAttribute("viewBox","0 0 "+W+" "+((maxDepth+1)*ROW));
  svg.setAttribute("height",((maxDepth+1)*ROW));
  var query=q.value.trim().toLowerCase(),matches=0,html="";
  for(i=0;i<items.length;i++){
    var it=items[i],den=node.value||1,frac=it.n.value/den,
        x=it.x/den*W,w=Math.max(frac*W,1),y=it.depth*ROW,
        pct=(100*it.n.value/total).toFixed(1),
        isMatch=query&&it.n.name.toLowerCase().indexOf(query)>=0;
    if(isMatch)matches++;
    var label=w>56?it.n.name.slice(0,Math.floor(w/7)):"";
    html+='<g><rect x="'+x.toFixed(1)+'" y="'+y+'" width="'+w.toFixed(1)+'" height="'+(ROW-1)+'" fill="'+color(it.n.name)+'"'+(isMatch?' class="match"':'')+' data-p="'+it.pp.join(",")+'"><title>'+it.n.name.replace(/&/g,"&amp;").replace(/</g,"&lt;")+'\n'+it.n.value+' ('+pct+'% of total)</title></rect>';
    if(label)html+='<text x="'+(x+4).toFixed(1)+'" y="'+(y+15)+'">'+label.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;")+'</text>';
    html+='</g>';
  }
  svg.innerHTML=html;
  if(query)svg.classList.add("dim");else svg.classList.remove("dim");
  var ns=names(),ch='<a data-p="">root</a>';
  for(i=0;i<ns.length;i++){ch+=' / <a data-p="'+path.slice(0,i+1).join(",")+'">'+ns[i].replace(/&/g,"&amp;").replace(/</g,"&lt;")+'</a>';}
  crumbs.innerHTML=ch;
  stats.textContent=items.length+" frames"+(query?" · "+matches+" matches":"")+" · total "+total;
}
svg.addEventListener("click",function(e){
  var r=e.target.closest("rect");if(!r||!r.getAttribute("data-p"))return;
  var p=r.getAttribute("data-p");path=p===""?[]:p.split(",").map(Number);draw();
});
crumbs.addEventListener("click",function(e){
  var a=e.target.closest("a");if(!a)return;
  var p=a.getAttribute("data-p");path=p===""?[]:p.split(",").map(Number);draw();
});
document.getElementById("reset").addEventListener("click",function(){path=[];draw();});
q.addEventListener("input",draw);
draw();
})();
</script>
</body>
</html>
`)
	return b.String(), nil
}

// RenderIndex builds the landing page that lists recent dashboards.
func RenderIndex(entries []IndexEntry) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Flamegraph dashboards — inspector-gopher</title>
<style>
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;margin:0 auto;max-width:900px;padding:16px 20px 48px;line-height:1.5}
table{border-collapse:collapse;width:100%;font-size:14px}
th,td{text-align:left;padding:6px 10px;border-bottom:1px solid #ddd}
td.id{font-family:ui-monospace,Menlo,Consolas,monospace}
code{background:#f2f2f2;padding:1px 6px;border-radius:4px}
</style>
</head>
<body>
<h1>Flamegraph dashboards</h1>
<p>Collect a profile (<code>profile_cpu</code>, <code>profile_heap</code>, …), then call
<code>flamegraph_report</code> with its <code>profile_id</code> to render an interactive
flamegraph here and as a saved HTML artifact.</p>`)
	if len(entries) == 0 {
		b.WriteString("\n<p>No profiles collected yet.</p>\n")
	} else {
		b.WriteString("\n<table><thead><tr><th>profile</th><th>target</th><th>kind</th><th>sample</th><th>dashboard</th></tr></thead><tbody>")
		for _, e := range entries {
			fmt.Fprintf(&b, "\n<tr><td class=\"id\">%s</td><td>%s</td><td>%s</td><td>%s</td><td><a href=\"%s\">open</a></td></tr>",
				html.EscapeString(e.ID), html.EscapeString(e.Target),
				html.EscapeString(e.Kind), html.EscapeString(e.SampleType),
				html.EscapeString(TemplatePrefix+e.ID))
		}
		b.WriteString("\n</tbody></table>\n")
	}
	b.WriteString(`</body>
</html>
`)
	return b.String()
}
