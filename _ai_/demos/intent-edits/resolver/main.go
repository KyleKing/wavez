// Command intentgo resolves a short intent line into a Go code edit:
// placement, imports, and a test stub, leaving only a marked hole.
// See DESIGN.md, "Intent edits".
package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: intentgo '<intent line>'")
		os.Exit(2)
	}
	line := strings.Join(os.Args[1:], " ")

	start := time.Now()
	it, err := ParseIntent(line)
	if err != nil {
		fmt.Fprintln(os.Stderr, "intentgo: parse error:", err)
		os.Exit(1)
	}

	r := newResult()
	switch it.Kind {
	case "add fn":
		err = runAddFn(it, r)
	case "add field":
		err = runAddField(it, r)
	case "like":
		err = runLike(it, r)
	}
	elapsed := time.Since(start)

	if err != nil {
		fmt.Fprintln(os.Stderr, "intentgo:", err)
		os.Exit(1)
	}

	r.PrintDiffs()
	r.PrintJSON()
	fmt.Fprintf(os.Stderr, "intentgo: %s in %s\n", it.Kind, elapsed)
}
