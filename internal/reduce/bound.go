package reduce

import (
	"fmt"
	"strings"
)

// headShare is the part of a bounded window given to the head. A compiler
// puts the first cause at the top and a test run puts the verdict at the
// bottom, so both ends are kept and the head gets the larger half.
const (
	headShare  = 3
	shareParts = 5
)

// Bound returns s when it fits in limit bytes and otherwise its head and
// tail around a marker saying how much was cut. The cuts fall on line
// boundaries where one is in reach, so no diagnostic arrives half-printed,
// and the marker's own bytes are spent on top of limit.
func Bound(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}

	head := wholeLinesBefore(s[:limit*headShare/shareParts])
	tail := wholeLinesAfter(s[len(s)-(limit-len(head)):])
	cut := len(s) - len(head) - len(tail)

	return fmt.Sprintf("%s\n... [%d bytes cut] ...\n%s", strings.TrimRight(head, "\n"), cut, tail)
}

// wholeLinesBefore trims s back to its last newline, or returns it whole
// when it holds none.
func wholeLinesBefore(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i > 0 {
		return s[:i+1]
	}

	return s
}

// wholeLinesAfter trims s forward to just past its first newline, or returns
// it whole when it holds none.
func wholeLinesAfter(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}

	return s
}
