// Demo service with net/http/pprof and an intentionally hot function for
// end-to-end profiling checks.
package main

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"regexp"
	"strings"
)

func hotLoop(s string) int {
	n := 0
	for i := 0; i < 200; i++ {
		// Deliberately wasteful: recompile per iteration.
		r := regexp.MustCompile(`[a-z]+@[0-9]+`)
		if r.MatchString(s) {
			n++
		}
		_ = strings.Repeat(s, 3)
	}
	return n
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "matches=%d\n", hotLoop("user42@example99 test"))
	})
	addr := "0.0.0.0:6060"
	log.Printf("demo listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
