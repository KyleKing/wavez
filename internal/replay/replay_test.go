package replay_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/replay"
)

var started = time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)

func writeTasks(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tasks.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing tasks: %v", err)
	}

	return path
}

func requireErr(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want one holding %q", err, want)
	}
}

func requireHolds(t *testing.T, got string, want ...string) {
	t.Helper()

	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output is missing %q:\n%s", w, got)
		}
	}
}

func TestLoadTasks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
		want    []replay.Task
	}{
		{
			name: "skips blanks and comments",
			body: "# a set\n\nq1|Answer something.\ne1|Change one line.\n",
			want: []replay.Task{{ID: "q1", Prompt: "Answer something."}, {ID: "e1", Prompt: "Change one line."}},
		},
		{name: "refuses a check without a substring", body: "q1|do it|README.md\n", wantErr: "not path:substring"},
		{name: "refuses a line without a prompt", body: "q1|\n", wantErr: "not id|prompt"},
		{name: "refuses a line without a separator", body: "q1 do a thing\n", wantErr: "not id|prompt"},
		{name: "refuses a repeated id", body: "q1|one\nq1|two\n", wantErr: "defined twice"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := replay.LoadTasks(writeTasks(t, tc.body))
			if tc.wantErr != "" {
				requireErr(t, err, tc.wantErr)

				return
			}
			if err != nil {
				t.Fatalf("LoadTasks: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d tasks, want %d", len(got), len(tc.want))
			}
			for i, w := range tc.want {
				if got[i].ID != w.ID || got[i].Prompt != w.Prompt {
					t.Errorf("task %d = %+v, want %+v", i, got[i], w)
				}
			}
		})
	}
}

func TestFindNamesTheTasksItHas(t *testing.T) {
	t.Parallel()

	tasks := []replay.Task{{ID: "q1", Prompt: "one"}, {ID: "e1", Prompt: "two"}}

	got, err := replay.Find(tasks, "e1")
	if err != nil || got.Prompt != "two" {
		t.Fatalf("Find(e1) = %+v, %v", got, err)
	}

	_, err = replay.Find(tasks, "nope")
	requireErr(t, err, "q1, e1")
}

func TestRecordsRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "records.jsonl")

	if got, err := replay.Load(path); err != nil || got != nil {
		t.Fatalf("Load of a missing file = %v, %v, want nil, nil", got, err)
	}

	for _, r := range fixtureRecords() {
		if err := replay.Append(path, r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	loaded, err := replay.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("loaded %d records, want 3", len(loaded))
	}
	if !loaded[0].Complete || loaded[1].Complete {
		t.Errorf("Complete tracks the stop reason: got %v, %v", loaded[0].Complete, loaded[1].Complete)
	}
	if kept := replay.ForTask(loaded, "e1"); len(kept) != 2 {
		t.Errorf("ForTask(e1) kept %d records, want 2", len(kept))
	}
}

func fixtureRecords() []replay.Record {
	return []replay.Record{
		replay.NewRecord(replay.Run{Task: "e1", Label: "before", Model: "balanced", MaxTurns: 60},
			started, "complete", bench.Stats{Turns: 30, ToolCalls: 40, InputTokens: 900}, nil),
		replay.NewRecord(replay.Run{Task: "q1", Label: "before"},
			started, "max_turns", bench.Stats{Turns: 60}, nil),
		replay.NewRecord(replay.Run{Task: "e1", Label: "after", Model: "balanced", MaxTurns: 60},
			started, "complete", bench.Stats{Turns: 12, ToolCalls: 15, InputTokens: 400},
			[]replay.CheckResult{{Check: "a:b", Pass: true}, {Check: "c:d"}}),
	}
}

func TestReportDiffsTheLastTwoRunsOfOneTask(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	if err := replay.Report(fixtureRecords(), "e1", &out); err != nil {
		t.Fatalf("Report: %v", err)
	}

	requireHolds(t, out.String(), "task e1, 2 run(s)", "before -> after", "turns", "-18", "1/2")
	if strings.Contains(out.String(), "max_turns") {
		t.Errorf("report leaked another task's run:\n%s", out.String())
	}
	if strings.Contains(out.String(), "setup differs") {
		t.Errorf("two runs of one setup should not be flagged:\n%s", out.String())
	}
}

func TestReportSkipsARunThatNeverTookATurn(t *testing.T) {
	t.Parallel()

	recs := []replay.Record{
		fixtureRecords()[0],
		replay.NewRecord(replay.Run{Task: "e1", Label: "killed", Model: "balanced", MaxTurns: 60},
			started, "error", bench.Stats{}, nil),
		fixtureRecords()[2],
	}

	var out strings.Builder
	if err := replay.Report(recs, "e1", &out); err != nil {
		t.Fatalf("Report: %v", err)
	}

	requireHolds(t, out.String(), "task e1, 3 run(s)", "killed", "before -> after", "-18")
}

func TestReportFlagsAPairRunUnderDifferentSetups(t *testing.T) {
	t.Parallel()

	recs := []replay.Record{
		fixtureRecords()[0],
		replay.NewRecord(replay.Run{Task: "e1", Label: "after", Model: "local", MaxTurns: 30},
			started, "complete", bench.Stats{Turns: 12}, nil),
	}

	var out strings.Builder
	if err := replay.Report(recs, "e1", &out); err != nil {
		t.Fatalf("Report: %v", err)
	}

	requireHolds(t, out.String(), "setup differs (model balanced vs local, max-turns 60 vs 30)")
}

func TestReportOnAnUnrunTask(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	if err := replay.Report(fixtureRecords(), "zz", &out); err != nil {
		t.Fatalf("Report: %v", err)
	}

	requireHolds(t, out.String(), "no runs recorded for task zz")
}

func TestVerifyChecksTheTreeAndTheAnswer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "memory.go"), []byte("func UsedFraction() {}\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	task := replay.Task{ID: "e2", Checks: []replay.Check{
		{Path: "memory.go", Want: "func UsedFraction"},
		{Path: "memory.go", Want: "firstDir", Negate: true},
		{Path: "memory.go", Want: "absent"},
		{Path: "gone.go", Want: "anything"},
		{Path: replay.AnswerPath, Want: "rules.go"},
	}}

	got := replay.Verify(task, dir, "the answer names rules.go")

	want := []bool{true, true, false, false, true}
	for i, w := range want {
		if got[i].Pass != w {
			t.Errorf("check %d (%s) pass = %v, want %v (%s)", i, got[i].Check, got[i].Pass, w, got[i].Note)
		}
	}
	if replay.Passed(got) {
		t.Error("Passed reported a set holding two failures")
	}
	if !strings.Contains(got[3].Note, "cannot read") {
		t.Errorf("a missing file is reported as %q, want it to name the read failure", got[3].Note)
	}
}

func TestReportSaysTheTaskTextChanged(t *testing.T) {
	t.Parallel()

	recs := []replay.Record{
		replay.NewRecord(replay.Run{Task: "e1", Label: "before", TaskHash: "aaaaaaaa"},
			started, "complete", bench.Stats{Turns: 4}, nil),
		replay.NewRecord(replay.Run{Task: "e1", Label: "after", TaskHash: "bbbbbbbb"},
			started, "complete", bench.Stats{Turns: 2}, nil),
	}

	var out strings.Builder
	if err := replay.Report(recs, "e1", &out); err != nil {
		t.Fatalf("Report: %v", err)
	}

	requireHolds(t, out.String(), "the task text changed between these runs (aaaaaaaa then bbbbbbbb)")
}
