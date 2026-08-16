package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// toolResult is what a tool call produces: text fed back to the model, plus
// whether the call was malformed (bad JSON, unknown tool, missing args,
// stale hash, no match) for logging.
type toolResult struct {
	Output    string
	Malformed bool
	Reason    string
}

func lineHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:2]
}

// --- read_file: shared by both formats, rendering differs ---

func readFilePlain(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readFileHashline(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%d#%s| %s\n", i+1, lineHash(line), line)
	}
	return b.String(), nil
}

// --- str_replace format ---

func editStrReplace(path, oldString, newString string) toolResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return toolResult{Malformed: true, Reason: "read error: " + err.Error(), Output: "error: " + err.Error()}
	}
	content := string(data)

	count := strings.Count(content, oldString)
	if count == 0 {
		return toolResult{Malformed: true, Reason: "no_match", Output: "error: old_string not found in file"}
	}
	if count > 1 {
		return toolResult{Malformed: true, Reason: "no_match", Output: fmt.Sprintf("error: old_string matches %d times, must match exactly once", count)}
	}

	updated := strings.Replace(content, oldString, newString, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return toolResult{Malformed: true, Reason: "write error: " + err.Error(), Output: "error: " + err.Error()}
	}

	return toolResult{Output: "ok: edit applied"}
}

// --- hashline format ---

type hashlineOp struct {
	Op    string   `json:"op"`
	Start string   `json:"start"`
	End   string   `json:"end,omitempty"`
	Lines []string `json:"lines,omitempty"`
}

type parsedAnchor struct {
	lineNum int
	hash    string
}

func parseAnchor(s string) (parsedAnchor, error) {
	parts := strings.SplitN(s, "#", 2)
	if len(parts) != 2 {
		return parsedAnchor{}, fmt.Errorf("malformed anchor %q, want N#hh", s)
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return parsedAnchor{}, fmt.Errorf("malformed line number in anchor %q", s)
	}
	if len(parts[1]) != 2 {
		return parsedAnchor{}, fmt.Errorf("malformed hash in anchor %q, want 2 hex chars", s)
	}
	return parsedAnchor{lineNum: n, hash: parts[1]}, nil
}

func editHashline(path string, ops []hashlineOp) toolResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return toolResult{Malformed: true, Reason: "read error: " + err.Error(), Output: "error: " + err.Error()}
	}
	trailingNewline := strings.HasSuffix(string(data), "\n")
	lines := strings.Split(string(data), "\n")
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}

	type validatedOp struct {
		op       hashlineOp
		startIdx int
		endIdx   int
	}
	var validated []validatedOp

	for _, op := range ops {
		start, err := parseAnchor(op.Start)
		if err != nil {
			return toolResult{Malformed: true, Reason: "stale_hash", Output: "error: " + err.Error()}
		}
		if start.lineNum < 1 || start.lineNum > len(lines) {
			return toolResult{Malformed: true, Reason: "stale_hash", Output: fmt.Sprintf("error: start line %d out of range (file has %d lines)", start.lineNum, len(lines))}
		}
		if got := lineHash(lines[start.lineNum-1]); got != start.hash {
			return toolResult{Malformed: true, Reason: "stale_hash", Output: fmt.Sprintf("error: stale hash at line %d: anchor says %s, current is %d#%s", start.lineNum, start.hash, start.lineNum, got)}
		}

		endIdx := start.lineNum
		if op.End != "" {
			end, err := parseAnchor(op.End)
			if err != nil {
				return toolResult{Malformed: true, Reason: "stale_hash", Output: "error: " + err.Error()}
			}
			if end.lineNum < start.lineNum || end.lineNum > len(lines) {
				return toolResult{Malformed: true, Reason: "stale_hash", Output: fmt.Sprintf("error: end line %d out of range", end.lineNum)}
			}
			if got := lineHash(lines[end.lineNum-1]); got != end.hash {
				return toolResult{Malformed: true, Reason: "stale_hash", Output: fmt.Sprintf("error: stale hash at line %d: anchor says %s, current is %d#%s", end.lineNum, end.hash, end.lineNum, got)}
			}
			endIdx = end.lineNum
		}

		switch op.Op {
		case "replace", "insert_after", "delete":
		default:
			return toolResult{Malformed: true, Reason: "unknown_op", Output: "error: unknown op " + op.Op}
		}

		validated = append(validated, validatedOp{op: op, startIdx: start.lineNum, endIdx: endIdx})
	}

	// Apply bottom-up so earlier edits don't shift line numbers used by later ones.
	for i := len(validated) - 1; i >= 0; i-- {
		v := validated[i]
		switch v.op.Op {
		case "replace":
			lines = append(lines[:v.startIdx-1], append(append([]string{}, v.op.Lines...), lines[v.endIdx:]...)...)
		case "insert_after":
			lines = append(lines[:v.startIdx], append(append([]string{}, v.op.Lines...), lines[v.startIdx:]...)...)
		case "delete":
			lines = append(lines[:v.startIdx-1], lines[v.endIdx:]...)
		}
	}

	out := strings.Join(lines, "\n")
	if trailingNewline {
		out += "\n"
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return toolResult{Malformed: true, Reason: "write error: " + err.Error(), Output: "error: " + err.Error()}
	}

	return toolResult{Output: fmt.Sprintf("ok: %d op(s) applied", len(validated))}
}

// --- run tool: restricted to build+vet on a given module dir ---

func runBuildVet(moduleDir string) toolResult {
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = moduleDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return toolResult{Output: "build failed:\n" + string(out)}
	}

	cmd = exec.Command("go", "vet", "./...")
	cmd.Dir = moduleDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		return toolResult{Output: "vet failed:\n" + string(out)}
	}

	return toolResult{Output: "build and vet passed"}
}
