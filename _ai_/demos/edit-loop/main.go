// Command editloop drives qwen3:8b through Ollama's /api/chat tool-use loop
// to compare two edit-tool formats (str_replace vs hashline) on small Go
// editing tasks, logging every turn.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	model        = "qwen3:8b"
	maxTurns     = 12
	ollamaAddr   = "http://localhost:11434"
	systemPrompt = "You are editing a single Go file to satisfy a task spec. " +
		"Use the tools to read the file, make the smallest edit that satisfies the spec, " +
		"run build_vet to confirm it compiles and vets cleanly, then call done with a one-line summary. " +
		"Only edit the target file. Do not explain yourself, just call tools."
)

type task struct {
	name       string
	moduleDir  string // testdata/taskN
	targetFile string // relative to moduleDir
	specPath   string
}

var tasks = []task{
	{"T1", "task1", "sort.go", "tasks/T1.md"},
	{"T2", "task2", "palette.go", "tasks/T2.md"},
	{"T3", "task3", "predicate.go", "tasks/T3.md"},
	{"T4", "task4", "palette.go", "tasks/T4.md"},
	{"T5", "task5", "search.go", "tasks/T5.md"},
}

type runSummary struct {
	Format          string  `json:"format"`
	Task            string  `json:"task"`
	Run             int     `json:"run"`
	Success         bool    `json:"success"`
	BuildVetPassed  bool    `json:"build_vet_passed"`
	Turns           int     `json:"turns"`
	MalformedCalls  int     `json:"malformed_calls"`
	StaleHashOrNoMatch int  `json:"stale_hash_or_no_match"`
	RepeatedCallStop bool   `json:"repeated_call_stop"`
	MaxTurnsHit     bool    `json:"max_turns_hit"`
	TotalPromptTok  int     `json:"total_prompt_tokens"`
	TotalOutputTok  int     `json:"total_output_tokens"`
	TotalWallMs     int64   `json:"total_wall_ms"`
	FailureNote     string  `json:"failure_note,omitempty"`
}

func main() {
	format := flag.String("format", "", "str_replace or hashline")
	taskName := flag.String("task", "", "task name, e.g. T1")
	runIdx := flag.Int("run", 1, "run index, 1 or 2")
	rootDir := flag.String("root", ".", "edit-loop root dir")
	flag.Parse()

	if *format != "str_replace" && *format != "hashline" {
		log.Fatalf("format must be str_replace or hashline, got %q", *format)
	}

	var tk *task
	for i := range tasks {
		if tasks[i].name == *taskName {
			tk = &tasks[i]
		}
	}
	if tk == nil {
		log.Fatalf("unknown task %q", *taskName)
	}

	root, err := filepath.Abs(*rootDir)
	if err != nil {
		log.Fatal(err)
	}

	summary := runOnce(root, *format, *tk, *runIdx)

	logsDir := filepath.Join(root, "logs")
	os.MkdirAll(logsDir, 0o755)
	resultsPath := filepath.Join(logsDir, "results.jsonl")
	f, err := os.OpenFile(resultsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(summary); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%+v\n", summary)
}

func resetModule(root string, tk task) string {
	pristine := filepath.Join(root, "testdata", ".pristine", tk.moduleDir)
	live := filepath.Join(root, "testdata", tk.moduleDir)
	os.RemoveAll(live)
	if err := copyDir(pristine, live); err != nil {
		log.Fatalf("reset module: %v", err)
	}
	return live
}

func runOnce(root, format string, tk task, runIdx int) runSummary {
	moduleDir := resetModule(root, tk)
	specBytes, err := os.ReadFile(filepath.Join(root, tk.specPath))
	if err != nil {
		log.Fatal(err)
	}

	logPath := filepath.Join(root, "logs", fmt.Sprintf("%s-%s-run%d.jsonl", format, tk.name, runIdx))
	logf, err := os.Create(logPath)
	if err != nil {
		log.Fatal(err)
	}
	defer logf.Close()
	logEnc := json.NewEncoder(logf)

	client := newOllamaClient(ollamaAddr)
	tools := toolsFor(format)

	userMsg := fmt.Sprintf(
		"Task spec:\n%s\n\nTarget file (relative to module root): %s\nModule root for the run tool: %s",
		string(specBytes), tk.targetFile, moduleDir,
	)

	messages := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}

	summary := runSummary{Format: format, Task: tk.name, Run: runIdx}
	var lastCallSig string
	doneCalled := false

	for turn := 1; turn <= maxTurns; turn++ {
		resp, elapsed, err := client.chat(model, messages, tools)
		summary.TotalWallMs += elapsed.Milliseconds()
		if err != nil {
			logEnc.Encode(map[string]any{"turn": turn, "error": err.Error()})
			summary.FailureNote = "ollama error: " + err.Error()
			break
		}
		summary.TotalPromptTok += resp.PromptEvalCount
		summary.TotalOutputTok += resp.EvalCount
		summary.Turns = turn

		logEnc.Encode(map[string]any{
			"turn":              turn,
			"content":           resp.Message.Content,
			"tool_calls":        resp.Message.ToolCalls,
			"prompt_eval_count": resp.PromptEvalCount,
			"eval_count":        resp.EvalCount,
			"wall_ms":           elapsed.Milliseconds(),
		})

		messages = append(messages, resp.Message)

		if len(resp.Message.ToolCalls) == 0 {
			messages = append(messages, chatMessage{
				Role:    "user",
				Content: "Call a tool: read_file, edit_file, run, or done.",
			})
			continue
		}

		stop := false
		for _, tc := range resp.Message.ToolCalls {
			sig := tc.Function.Name + "|" + fmt.Sprint(tc.Function.Arguments)
			repeated := sig == lastCallSig
			lastCallSig = sig

			result := dispatchTool(format, moduleDir, tk, tc)
			if result.Malformed {
				summary.MalformedCalls++
				if result.Reason == "stale_hash" || result.Reason == "no_match" {
					summary.StaleHashOrNoMatch++
				}
			}
			logEnc.Encode(map[string]any{
				"turn":      turn,
				"tool":      tc.Function.Name,
				"args":      tc.Function.Arguments,
				"malformed": result.Malformed,
				"reason":    result.Reason,
				"output":    result.Output,
				"repeated":  repeated,
			})

			messages = append(messages, chatMessage{
				Role:     "tool",
				ToolName: tc.Function.Name,
				Content:  result.Output,
			})

			if repeated {
				summary.RepeatedCallStop = true
				stop = true
			}
			if tc.Function.Name == "done" {
				doneCalled = true
				stop = true
			}
		}
		if stop {
			break
		}
	}

	if summary.Turns >= maxTurns && !doneCalled {
		summary.MaxTurnsHit = true
	}

	vetResult := runBuildVet(moduleDir)
	summary.BuildVetPassed = vetResult.Output == "build and vet passed"
	summary.Success = doneCalled && summary.BuildVetPassed && !summary.RepeatedCallStop

	logEnc.Encode(map[string]any{"final_build_vet": vetResult.Output})

	return summary
}

func dispatchTool(format, moduleDir string, tk task, tc toolCall) toolResult {
	args := tc.Function.Arguments

	switch tc.Function.Name {
	case "read_file":
		path, _ := args["path"].(string)
		if path == "" {
			return toolResult{Malformed: true, Reason: "missing_args", Output: "error: missing path"}
		}
		full := resolvePath(moduleDir, path)
		var content string
		var err error
		if format == "hashline" {
			content, err = readFileHashline(full)
		} else {
			content, err = readFilePlain(full)
		}
		if err != nil {
			return toolResult{Malformed: true, Reason: "missing_args", Output: "error: " + err.Error()}
		}
		return toolResult{Output: content}

	case "edit_file":
		path, _ := args["path"].(string)
		if path == "" {
			return toolResult{Malformed: true, Reason: "missing_args", Output: "error: missing path"}
		}
		full := resolvePath(moduleDir, path)

		if format == "str_replace" {
			oldS, ok1 := args["old_string"].(string)
			newS, ok2 := args["new_string"].(string)
			if !ok1 || !ok2 {
				return toolResult{Malformed: true, Reason: "missing_args", Output: "error: missing old_string or new_string"}
			}
			return editStrReplace(full, oldS, newS)
		}

		rawOps, ok := args["ops"]
		if !ok {
			return toolResult{Malformed: true, Reason: "missing_args", Output: "error: missing ops"}
		}
		opsJSON, err := json.Marshal(rawOps)
		if err != nil {
			return toolResult{Malformed: true, Reason: "bad_json", Output: "error: " + err.Error()}
		}
		var ops []hashlineOp
		if err := json.Unmarshal(opsJSON, &ops); err != nil {
			return toolResult{Malformed: true, Reason: "bad_json", Output: "error: malformed ops: " + err.Error()}
		}
		return editHashline(full, ops)

	case "run":
		cmdStr, _ := args["cmd"].(string)
		normalized := strings.TrimSpace(cmdStr)
		if normalized != "" && normalized != "go build ./... && go vet ./..." {
			return toolResult{Malformed: true, Reason: "unknown_tool", Output: "error: only 'go build ./... && go vet ./...' is allowed"}
		}
		return runBuildVet(moduleDir)

	case "done":
		summaryText, _ := args["summary"].(string)
		if summaryText == "" {
			return toolResult{Malformed: true, Reason: "missing_args", Output: "error: missing summary"}
		}
		return toolResult{Output: "ok: run complete"}

	default:
		return toolResult{Malformed: true, Reason: "unknown_tool", Output: "error: unknown tool " + tc.Function.Name}
	}
}

func resolvePath(moduleDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if strings.HasPrefix(path, moduleDir) {
		return path
	}
	return filepath.Join(moduleDir, filepath.Base(path))
}

func toolsFor(format string) []toolDef {
	readTool := toolDef{
		Type: "function",
		Function: functionSpec{
			Name:        "read_file",
			Description: "Read the target Go file. Returns its full contents.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string", "description": "file path"}},
				"required":   []string{"path"},
			},
		},
	}
	runTool := toolDef{
		Type: "function",
		Function: functionSpec{
			Name:        "run",
			Description: "Run 'go build ./... && go vet ./...' on the module. This is the only allowed command.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"cmd": map[string]any{"type": "string", "description": "must be exactly: go build ./... && go vet ./..."}},
				"required":   []string{"cmd"},
			},
		},
	}
	doneTool := toolDef{
		Type: "function",
		Function: functionSpec{
			Name:        "done",
			Description: "Call this when the edit is complete and verified.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"summary": map[string]any{"type": "string", "description": "one-line summary of the change made"}},
				"required":   []string{"summary"},
			},
		},
	}

	var editTool toolDef
	if format == "str_replace" {
		editTool = toolDef{
			Type: "function",
			Function: functionSpec{
				Name: "edit_file",
				Description: "Replace an exact substring in the file. old_string must match exactly once in the current file contents, " +
					"including whitespace and indentation. If it matches zero or multiple times, the edit is rejected.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":       map[string]any{"type": "string"},
						"old_string": map[string]any{"type": "string", "description": "exact text to find, must be unique in the file"},
						"new_string": map[string]any{"type": "string", "description": "text to replace it with"},
					},
					"required": []string{"path", "old_string", "new_string"},
				},
			},
		}
	} else {
		editTool = toolDef{
			Type: "function",
			Function: functionSpec{
				Name: "edit_file",
				Description: "Apply line-anchored edit ops to the file. Each anchor is \"N#hh\" (line number, then 2-char content hash) " +
					"exactly as shown by read_file. Every op's hash must match the file's current content or the whole call is rejected. " +
					"op is one of: replace (replace lines start..end with 'lines'), insert_after (insert 'lines' after 'start', end is ignored), " +
					"delete (delete lines start..end, 'lines' is ignored).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
						"ops": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"op":    map[string]any{"type": "string", "enum": []string{"replace", "insert_after", "delete"}},
									"start": map[string]any{"type": "string", "description": "anchor N#hh"},
									"end":   map[string]any{"type": "string", "description": "anchor N#hh, optional, defaults to start"},
									"lines": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								},
								"required": []string{"op", "start"},
							},
						},
					},
					"required": []string{"path", "ops"},
				},
			},
		}
	}

	return []toolDef{readTool, editTool, runTool, doneTool}
}
