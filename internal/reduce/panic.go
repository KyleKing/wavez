package reduce

import (
	"regexp"
	"strings"
)

const (
	// A panic keeps panicMessageLines of what it said and panicFrames of the
	// places it passed through inside the project.
	panicMessageLines = 8
	panicFrames       = 3
	// The framePrefix opens a frame this package already collapsed, so that a
	// second pass leaves it alone.
	framePrefix = "\tat "
	// The timeoutPanic line opens the panic `go test -timeout` raises.
	timeoutPanic = "panic: test timed out after"
)

var (
	// The frameSite pattern matches the file line of a stack frame and captures its path
	// and line number, without the program counter that follows.
	frameSite = regexp.MustCompile(`^\t(\S+\.go:\d+)`)
	// The frameArgs pattern matches the argument words of a frame's function line.
	frameArgs = regexp.MustCompile(`\(0x[0-9a-fx, .?{}]*\)$|\(\.{3}\)$|\(\)$`)
	// The timedOutTest pattern matches one entry of the list a timeout panic prints under
	// "running tests:" and captures the test's full name.
	timedOutTest = regexp.MustCompile(`^\t\t(\S+) \(\S+\)$`)
)

// isPanic reports whether line opens what the runtime prints when a program
// dies: a panic, or a fatal error such as a deadlock.
func isPanic(line string) bool {
	return strings.HasPrefix(line, "panic: ") || strings.HasPrefix(line, "fatal error: ")
}

// panicEnd returns the index just past the panic that starts at lines[at]:
// the goroutine dump runs to the package's verdict, or to the end.
func panicEnd(lines []string, at int) int {
	for i := at + 1; i < len(lines); i++ {
		line := lines[i]

		if failVerdict.MatchString(line) || packageVerdict.MatchString(line) || line == bareFail ||
			strings.HasPrefix(line, "exit status ") || isFailMarker(strings.TrimLeft(line, " \t")) {
			return i
		}
	}

	return len(lines)
}

// collapsePanic reduces a panic and its goroutine dump to the message and
// the first frames whose source is inside the project. A frame is inside
// when its path is relative, which is what Failure makes of a path under the
// project root; the runtime's and the toolchain's frames are absolute and
// say nothing a reader can change.
func collapsePanic(block []string) []string {
	var out []string

	at := 0
	for ; at < len(block) && len(out) < panicMessageLines; at++ {
		line := block[at]
		if at > 0 && (strings.TrimSpace(line) == "" || strings.HasPrefix(line, "goroutine ") ||
			strings.HasPrefix(line, framePrefix)) {
			break
		}

		out = append(out, line)
	}

	frames := 0

	for i := at; i < len(block) && frames < panicFrames; i++ {
		if strings.HasPrefix(block[i], framePrefix) {
			out = append(out, block[i])
			frames++

			continue
		}

		site := frameSite.FindStringSubmatch(block[i])
		if site == nil || i == 0 || strings.HasPrefix(site[1], "/") || strings.HasPrefix(site[1], "_testmain.go") {
			continue
		}

		function := frameArgs.ReplaceAllString(strings.TrimSpace(block[i-1]), "")
		out = append(out, framePrefix+function+" ("+site[1]+")")
		frames++
	}

	return out
}

// timedOut returns the tests a timeout panic in lines says were running.
func timedOut(lines []string) []string {
	var names []string

	for i, line := range lines {
		if !strings.HasPrefix(line, timeoutPanic) {
			continue
		}

		for _, entry := range lines[i+1:] {
			if m := timedOutTest.FindStringSubmatch(entry); m != nil {
				names = append(names, m[1])
			} else if strings.TrimSpace(entry) != "running tests:" {
				break
			}
		}
	}

	return names
}
