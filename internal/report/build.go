package report

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/kyleking/wavez/internal/transcript"
)

// look is how one kind of step appears on the page.
type look struct {
	letter string
	name   string
}

// looks gives every kind a letter beside its color, so the color is never
// the only thing telling two kinds apart.
var looks = map[transcript.Kind]look{
	transcript.KindRetrieval: {"R", "Retrieval"},
	transcript.KindEdit:      {"E", "Edit"},
	transcript.KindShell:     {"S", "Shell"},
	transcript.KindSweep:     {"W", "Whole-project run"},
	transcript.KindRefactor:  {"F", "Refactor command"},
	transcript.KindOther:     {"O", "Other tool"},
	transcript.KindProse:     {"T", "Text only"},
	transcript.KindPrompt:    {"1", "Prompt, numbered"},
	transcript.KindHook:      {"G", "Stop hooks ran"},
	transcript.KindCompact:   {"C", "Compaction"},
}

// The marks a cell can carry beside its kind.
const (
	markFailed  = "failed"
	markDenied  = "denied"
	markBlocked = "blocked"
)

// Build lays a session out as a page.
func Build(in *Input) Page {
	tl := &in.Timeline
	page := Page{
		Project: in.Project,
		Dir:     tl.Cwd,
		Session: tl.SessionID,
		Short:   short(tl.SessionID),
		Date:    NotRecorded,
		Full:    in.Full,
	}

	if !tl.Start.IsZero() {
		page.Date = tl.Start.UTC().Format(dateLayout)
	}

	page.Stats = stats(tl)
	page.Goal = goalView(in.Goal)
	page.Needs = needs(in)
	page.Cells = cells(tl, in.Full)
	page.Shares, page.Kinds = byKind(tl)
	page.Legend = legend(tl)
	page.Segments = segments(tl)
	page.Facts = facts(in)
	page.Waste = waste(in)
	page.Gates = gates(tl, in.Full)

	if len(page.Gates) == 0 {
		page.GateNote = "The transcript records no Stop hook run."
	}

	page.Summary = summary(&page)

	return page
}

func stats(tl *transcript.SessionTimeline) []Fact {
	cost := Fact{Label: "Cost", Value: NotRecorded}
	if tl.Cost.Recorded {
		cost.Value = dollars(tl.Cost.USD)

		if tl.Cost.Partial {
			cost.Note = "one model's price was unknown"
		}
	}

	models := NotRecorded
	if len(tl.Models) > 0 {
		models = strings.Join(tl.Models, ", ")
	}

	steps := Fact{Label: "Steps", Value: number(tl.WorkSteps())}
	if tl.Subagents > 0 {
		steps.Note = "plus " + number(tl.SubagentSteps) + " in " + plural(tl.Subagents, "subagent")
	}

	return []Fact{
		{Label: "Duration", Value: span(tl.End.Sub(tl.Start))},
		{Label: "Turns", Value: number(len(tl.Turns))},
		steps,
		cost,
		{Label: "Model", Value: models},
	}
}

func goalView(goal *Goal) *GoalView {
	if goal == nil {
		return nil
	}

	return &GoalView{
		Sentence: goal.Sentence,
		Progress: number(goal.Done) + " of " + number(goal.Total) + " done",
		Unmet:    goal.Unmet,
	}
}

func cells(tl *transcript.SessionTimeline, full bool) []Cell {
	out := make([]Cell, 0, len(tl.Steps))

	for i := range tl.Steps {
		step := &tl.Steps[i]
		cell := Cell{
			Letter: looks[step.Kind].letter,
			Class:  "k-" + string(step.Kind),
			Title:  "#" + strconv.Itoa(step.Index+1) + " " + step.Label,
		}

		if step.Kind == transcript.KindPrompt {
			cell.Letter = strconv.Itoa(step.Prompt)
			cell.Title = "Prompt " + cell.Letter + ": " + step.Label
		}

		if step.Duration > 0 {
			cell.Title += " (" + span(step.Duration) + ")"
		}

		switch {
		case step.Blocked:
			cell.Mark, cell.Letter = markBlocked, "!"
		case step.Denied:
			cell.Mark = markDenied
		case step.Failed:
			cell.Mark = markFailed
		}

		if cell.Mark != "" {
			cell.Title += " [" + cell.Mark + "]"
		}

		if full {
			cell.Detail = step.Detail
		}

		out = append(out, cell)
	}

	return out
}

// kindTotal is what one kind of step added up to.
type kindTotal struct {
	usage transcript.Usage
	steps int
	turns int
}

// byKind splits the agent's steps and its turns' tokens by kind. A turn's
// tokens go to the strongest thing the turn did.
func byKind(tl *transcript.SessionTimeline) ([]Share, []KindRow) {
	totals := map[transcript.Kind]*kindTotal{}
	get := func(k transcript.Kind) *kindTotal {
		if totals[k] == nil {
			totals[k] = &kindTotal{}
		}

		return totals[k]
	}

	all, metered := kindTotal{}, 0

	for i := range tl.Steps {
		if k := tl.Steps[i].Kind; k.Work() {
			get(k).steps++
			all.steps++
		}
	}

	for i := range tl.Turns {
		turn := &tl.Turns[i]
		get(turn.Kind).turns++
		all.turns++

		if turn.HasUsage {
			metered++
			get(turn.Kind).usage = sum(get(turn.Kind).usage, turn.Usage)
			all.usage = sum(all.usage, turn.Usage)
		}
	}

	var (
		shares []Share
		rows   []KindRow
	)

	for _, k := range transcript.Kinds() {
		total := totals[k]
		if !k.Work() || total == nil {
			continue
		}

		row := kindRow(k, total, all.steps, metered > 0)
		rows = append(rows, row)

		if total.steps > 0 {
			shares = append(shares, Share{
				Letter: row.Letter, Class: row.Class, Name: row.Name, Count: row.Steps, Percent: row.Share,
				Width: strconv.FormatFloat(float64(total.steps)*hundred/float64(all.steps), 'f', 2, 64) + "%",
			})
		}
	}

	if len(rows) > 0 {
		row := kindRow("", &all, all.steps, metered > 0)
		row.Name, row.Class, row.Total = "All", "", true
		rows = append(rows, row)
	}

	return shares, rows
}

func kindRow(k transcript.Kind, total *kindTotal, steps int, metered bool) KindRow {
	row := KindRow{
		Letter: looks[k].letter,
		Class:  "k-" + string(k),
		Name:   looks[k].name,
		Steps:  number(total.steps),
		Share:  percent(total.steps, steps),
		Turns:  number(total.turns),
		Output: NotRecorded,
		Input:  NotRecorded,
	}

	if metered {
		row.Output = number(total.usage.Output)
		row.Input = number(total.usage.Context())
	}

	return row
}

func sum(a, b transcript.Usage) transcript.Usage {
	return transcript.Usage{
		Input:         a.Input + b.Input,
		CacheRead:     a.CacheRead + b.CacheRead,
		CacheCreation: a.CacheCreation + b.CacheCreation,
		Output:        a.Output + b.Output,
	}
}

// legend lists the marks on this session's strip that the share key
// beside it does not already explain.
func legend(tl *transcript.SessionTimeline) []Legend {
	present := map[transcript.Kind]bool{}
	marks := map[string]bool{}

	for i := range tl.Steps {
		step := &tl.Steps[i]
		present[step.Kind] = true
		marks[markBlocked] = marks[markBlocked] || step.Blocked
		marks[markFailed] = marks[markFailed] || step.Failed || step.Denied
	}

	var out []Legend

	for _, k := range transcript.Kinds() {
		if present[k] && !k.Work() {
			out = append(out, Legend{Letter: looks[k].letter, Class: "k-" + string(k), Name: looks[k].name})
		}
	}

	if marks[markBlocked] {
		out = append(out, Legend{Letter: "!", Class: "k-hook m-blocked", Name: "Stop blocked"})
	}

	if marks[markFailed] {
		name := "Underlined: the call failed or was denied"
		out = append(out, Legend{Letter: "S", Class: "k-shell m-failed", Name: name})
	}

	return out
}

func segments(tl *transcript.SessionTimeline) []SegmentRow {
	out := make([]SegmentRow, 0, len(tl.Segments))

	for i := range tl.Segments {
		seg := &tl.Segments[i]
		out = append(out, SegmentRow{
			Number: strconv.Itoa(seg.Prompt),
			Label:  seg.Label,
			Steps:  number(seg.Steps),
			Turns:  number(seg.Turns),
		})
	}

	return out
}

func facts(in *Input) []Fact {
	tl := &in.Timeline
	cache := Fact{Label: "Input read from cache", Value: NotRecorded}
	peak := Fact{Label: "Largest request", Value: NotRecorded}

	if tl.Usage.Context() > 0 {
		cache.Value = percent(tl.Usage.CacheRead, tl.Usage.Context())
		cache.Note = number(tl.Usage.CacheRead) + " of " + number(tl.Usage.Context()) + " input tokens"
		peak.Value = number(tl.PeakContext) + " tokens"
		peak.Note = "the most context one turn carried in"
	}

	model := Fact{Label: "Time in the model", Value: NotRecorded}
	tools := Fact{Label: "Time in tools", Value: NotRecorded}

	if tl.Cost.Recorded {
		model.Value, tools.Value = measured(tl.Cost.Model), measured(tl.Cost.Tools)
		model.Note = "as the harness counted it, subagents included"
	}

	out := []Fact{cache, peak, model, tools, hookText(tl)}

	hints, denials := tl.RouteHints, len(tl.Denials)
	if in.Tally != nil {
		hints, denials = max(hints, in.Tally.RouteHints), max(denials, in.Tally.RenameDenials)
	}

	if hints > 0 {
		out = append(out, Fact{Label: "Prompts given a hint", Value: number(hints)})
	}

	if denials > 0 {
		out = append(out, Fact{Label: "Text renames denied", Value: number(denials)})
	}

	changed := Fact{Label: "Files edited", Value: number(len(tl.Edited)), Note: "by the edit tools, per the transcript"}
	if in.State != nil {
		changed.Value, changed.Note = number(in.State.Changed), "as wavez recorded them"
	}

	return append(out, changed)
}

// hookText says how much text hooks handed the agent. A session with no
// trace of a hook says so instead of claiming none was handed over.
func hookText(tl *transcript.SessionTimeline) Fact {
	fact := Fact{Label: "Hook text handed to the agent", Value: NotRecorded}

	if len(tl.HookBytes) == 0 {
		if len(tl.Gates) > 0 {
			fact.Value, fact.Note = size(0), "the hooks ran and said nothing"
		}

		return fact
	}

	total := 0
	parts := make([]string, 0, len(tl.HookBytes))

	for _, event := range slices.Sorted(maps.Keys(tl.HookBytes)) {
		total += tl.HookBytes[event]
		parts = append(parts, event+" "+number(tl.HookBytes[event]))
	}

	fact.Value, fact.Note = size(total), strings.Join(parts, ", ")

	return fact
}

func gates(tl *transcript.SessionTimeline, full bool) []GateRow {
	out := make([]GateRow, 0, len(tl.Gates))

	for i := range tl.Gates {
		round := &tl.Gates[i]
		row := GateRow{
			Number:   strconv.Itoa(i + 1),
			At:       offset(tl.Start, round.Time),
			Outcome:  string(round.Outcome),
			Class:    "g-" + string(round.Outcome),
			Checks:   checkNames(round.Checks),
			Duration: span(round.Duration),
			Agent:    size(round.AgentBytes),
			Person:   size(round.PersonBytes),
			Said:     round.Headline,
		}

		if full {
			row.Said = strings.TrimSpace(round.Agent + "\n" + round.Person)
		}

		out = append(out, row)
	}

	return out
}

// checkNames lists the checks a round named. A round that named none says
// so, because a silent pass leaves no record of what ran.
func checkNames(checks []transcript.GateCheck) string {
	if len(checks) == 0 {
		return NotRecorded
	}

	var parts []string

	for _, c := range checks {
		if part := c.Name + " (" + c.Status + ")"; !slices.Contains(parts, part) {
			parts = append(parts, part)
		}
	}

	return strings.Join(parts, ", ")
}

// summary is the session in five lines of plain text.
func summary(p *Page) string {
	value := func(label string) string {
		for _, f := range p.Stats {
			if f.Label == label {
				return f.Value
			}
		}

		return NotRecorded
	}

	shares := make([]string, 0, len(p.Shares))
	for _, s := range p.Shares {
		shares = append(shares, strings.ToLower(s.Name)+" "+s.Count+" ("+s.Percent+")")
	}

	lines := []string{
		"Session " + p.Short + " in " + p.Project + ", " + p.Date,
		counted(value("Turns"), "turn") + " and " + counted(value("Steps"), "step") + " in " + value("Duration") +
			", cost " + value("Cost") + ", model " + value("Model"),
		"Steps: " + strings.Join(shares, ", "),
		"Needs you: " + titles(p.Needs, "nothing"),
		"Waste: " + titles(p.Waste, "none found"),
	}

	return strings.Join(lines, "\n")
}

// counted puts a noun after a count that is already text.
func counted(n, noun string) string {
	if n == "1" {
		return n + " " + noun
	}

	return n + " " + noun + "s"
}

func titles(items []Item, none string) string {
	if len(items) == 0 {
		return none
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Title)
	}

	return strings.Join(out, "; ")
}
