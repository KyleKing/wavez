package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

const newFileMode = 0o644

// The seconds each installed hook may run before Claude Code cancels it.
// Left out, a command hook gets ten minutes, which is how long a wavez that
// hangs would hold every tool call.
const (
	// A tool call and a prompt wait on these two, and neither does more
	// than read a few files.
	promptTimeout = 10
	// An edit waits on the formatter, and a session's start on a snapshot
	// of what git says is dirty.
	editTimeout  = 40
	startTimeout = 30
	// The stopMargin is what the Stop hook gets on top of the gate's own
	// budget, for the work around the checks: finding the change set,
	// removing the baseline tree, writing the state.
	stopMargin = 30 * time.Second
)

// stopStatus is the spinner text Claude Code shows while the Stop hook
// runs, so that a pause at the end of a turn says what it is.
const stopStatus = "wavez: running checks"

// entry is one hook wavez installs.
type entry struct {
	event   string
	matcher string
	status  string
	timeout int
}

// entries lists the five hooks: PostToolUse on the edit tools, Stop,
// SessionStart, UserPromptSubmit, and PreToolUse on the shell tool. The
// UserPromptSubmit entry is installed whether or not the project has the
// route hint on, and the handler does nothing on that event until it is, so
// turning the hint on in .wavez.toml needs no second init. The Stop hook's
// timeout is stopBudget plus a margin, with zero meaning the default budget.
func entries(stopBudget time.Duration) []entry {
	if stopBudget <= 0 {
		stopBudget = gate.DefaultStopBudget
	}

	stop := int((stopBudget + stopMargin).Round(time.Second) / time.Second)

	return []entry{
		{event: eventPostToolUse, matcher: EditTools, timeout: editTimeout},
		{event: eventStop, timeout: stop, status: stopStatus},
		{event: eventSessionStart, timeout: startTimeout},
		{event: eventUserPromptSubmit, timeout: promptTimeout},
		{event: eventPreToolUse, matcher: ShellTool, timeout: promptTimeout},
	}
}

// settings is a parsed settings.json and the bytes it was parsed from.
type settings struct {
	doc *object
	raw []byte
}

func parseSettings(current []byte) (settings, error) {
	doc := newObject()

	if len(bytes.TrimSpace(current)) > 0 {
		parsed, err := decodeJSON(current)
		if err != nil {
			return settings{}, fmt.Errorf("%w: %w", ErrSettingsShape, err)
		}

		var ok bool
		if doc, ok = parsed.(*object); !ok {
			return settings{}, fmt.Errorf("%w: the top level is not an object", ErrSettingsShape)
		}
	}

	return settings{doc: doc, raw: current}, nil
}

// hooks returns the document's hooks object, or a new one it does not have
// yet.
func (s settings) hooks() (*object, error) {
	existing, ok := s.doc.get("hooks")
	if !ok {
		return newObject(), nil
	}

	hooks, ok := existing.(*object)
	if !ok {
		return nil, fmt.Errorf("%w: hooks is not an object", ErrSettingsShape)
	}

	return hooks, nil
}

// mergeSettings returns settings.json with wavez's hook entries in it.
// Every key and hook that is not wavez's own is kept as it was, in its
// place; an entry wavez installed earlier is updated where it stands, and
// surplus copies of it are removed. A nil current means there is no file.
// When the settings already say what init wants, current comes back
// byte for byte, and when they do not, only the hooks value is rewritten:
// the rest of the file keeps the layout its author gave it.
func mergeSettings(current []byte, command string, stopBudget time.Duration) ([]byte, error) {
	parsed, err := parseSettings(current)
	if err != nil {
		return nil, err
	}

	hooks, err := parsed.hooks()
	if err != nil {
		return nil, err
	}

	before, err := encodeJSON(hooks, defaultIndent)
	if err != nil {
		return nil, err
	}

	for _, want := range entries(stopBudget) {
		groups, err := groupsOf(hooks, want.event)
		if err != nil {
			return nil, err
		}

		merged, err := mergeEvent(groups, want, command)
		if err != nil {
			return nil, fmt.Errorf("hooks.%s: %w", want.event, err)
		}

		hooks.set(want.event, merged)
	}

	return parsed.with(hooks, before)
}

// unmergeSettings returns settings.json without the hooks init installed,
// and nothing else changed: another tool's hooks, a person's own, and every
// other key stay. An event left with no hooks goes, and so does a hooks
// object left with no events. A nil result means nothing is left of the
// file at all.
func unmergeSettings(current []byte, command string) ([]byte, error) {
	parsed, err := parseSettings(current)
	if err != nil {
		return nil, err
	}

	hooks, err := parsed.hooks()
	if err != nil {
		return nil, err
	}

	before, err := encodeJSON(hooks, defaultIndent)
	if err != nil {
		return nil, err
	}

	for _, installed := range entries(0) {
		groups, err := groupsOf(hooks, installed.event)
		if err != nil {
			return nil, err
		}

		kept, err := withoutOwn(groups, command)
		if err != nil {
			return nil, fmt.Errorf("hooks.%s: %w", installed.event, err)
		}

		if len(kept) == 0 {
			hooks.remove(installed.event)

			continue
		}

		hooks.set(installed.event, kept)
	}

	if len(hooks.keys) > 0 {
		return parsed.with(hooks, before)
	}

	if _, had := parsed.doc.get("hooks"); !had {
		return current, nil
	}

	parsed.doc.remove("hooks")
	if len(parsed.doc.keys) == 0 {
		return nil, nil
	}

	// The key is cut out of the file's own bytes, so the rest keeps its
	// layout. Where that cannot be done cleanly the file is laid out again.
	if cut, ok := withoutKey(current, "hooks"); ok {
		return cut, nil
	}

	return encodeJSON(parsed.doc, indentOf(current))
}

// withoutKey removes a top-level member from a JSON object's text: the key,
// its value, and the one comma that joined it to its neighbors. It reports
// false when what is left would not be JSON.
func withoutKey(raw []byte, key string) ([]byte, bool) {
	start, end, ok := valueSpan(raw, key)
	if !ok {
		return nil, false
	}

	at := bytes.LastIndex(raw[:start], []byte(`"`+key+`"`))
	if at < 0 {
		return nil, false
	}

	before := bytes.TrimRight(raw[:at], " \t\r\n")
	after := raw[end:]

	switch {
	case bytes.HasSuffix(before, []byte(",")):
		// Not the first member: it goes with the comma in front of it. An
		// object that was written on one line goes back onto one.
		if before = before[:len(before)-1]; !bytes.Contains(before, []byte("\n")) {
			after = bytes.TrimLeft(after, " \t\r\n")
		}
	case bytes.HasPrefix(bytes.TrimLeft(after, " \t\r\n"), []byte(",")):
		// The first of several: it goes with the comma behind it, and what
		// follows keeps the indentation the key had.
		before = raw[:at]
		after = bytes.TrimLeft(bytes.TrimLeft(after, " \t\r\n")[1:], " \t\r\n")
	}

	cut := slicesJoin(before, after)

	return cut, json.Valid(cut)
}

func groupsOf(hooks *object, event string) ([]any, error) {
	existing, ok := hooks.get(event)
	if !ok {
		return []any{}, nil
	}

	groups, ok := existing.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: hooks.%s is not an array", ErrSettingsShape, event)
	}

	return groups, nil
}

// with returns the file holding hooks in place of what it had. The bytes
// outside the hooks value are the file's own, so its indentation, its
// one-line arrays, and its key order all survive.
func (s settings) with(hooks *object, before []byte) ([]byte, error) {
	indent := indentOf(s.raw)

	after, err := encodeJSON(hooks, defaultIndent)
	if err != nil {
		return nil, err
	}

	// Nothing to change means the file's own formatting stays too.
	if s.raw != nil && bytes.Equal(before, after) {
		return s.raw, nil
	}

	var value bytes.Buffer
	if err := encodeValue(&value, hooks, layout{indent: indent, depth: 1}); err != nil {
		return nil, err
	}

	if start, end, ok := valueSpan(s.raw, "hooks"); ok {
		return slicesJoin(s.raw[:start], value.Bytes(), s.raw[end:]), nil
	}

	closing := bytes.LastIndexByte(s.raw, '}')
	if closing < 0 {
		s.doc.set("hooks", hooks)

		return encodeJSON(s.doc, indent)
	}

	head := bytes.TrimRight(s.raw[:closing], " \t\r\n")

	comma := ","
	if bytes.HasSuffix(head, []byte("{")) {
		comma = ""
	}

	member := comma + "\n" + indent + `"hooks": ` + value.String() + "\n"

	return slicesJoin(head, []byte(member), s.raw[closing:]), nil
}

func slicesJoin(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

// valueSpan finds the bytes of a top-level key's value in a JSON object.
func valueSpan(raw []byte, key string) (int, int, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))

	if _, err := dec.Token(); err != nil {
		return 0, 0, false
	}

	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return 0, 0, false
		}

		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return 0, 0, false
		}

		if name == key {
			end := int(dec.InputOffset())

			return end - len(value), end, true
		}
	}

	return 0, 0, false
}

// firstIndent matches the whitespace in front of the first indented key.
var firstIndent = regexp.MustCompile(`(?m)^([ \t]+)"`)

// indentOf is the indentation of one level in a settings file, taken from
// its first indented key, and Claude Code's own when it has none.
func indentOf(raw []byte) string {
	if m := firstIndent.FindSubmatch(raw); m != nil {
		return string(m[1])
	}

	return defaultIndent
}

// mergeEvent leaves exactly one wavez handler among an event's matcher
// groups. The first group that holds nothing but a wavez handler is updated
// in place, which keeps a timeout or a status message a person gave it; any
// other wavez handler is removed, and a group emptied that way goes with
// it.
func mergeEvent(groups []any, want entry, command string) ([]any, error) {
	merged := make([]any, 0, len(groups)+1)
	placed := false

	for _, item := range groups {
		group, ok := item.(*object)
		if !ok {
			return nil, fmt.Errorf("%w: a matcher group is not an object", ErrSettingsShape)
		}

		own, foreign := splitHandlers(group, command)

		switch {
		case len(own) == 0:
			// Not a group wavez has anything in.
		case len(foreign) == 0 && !placed:
			want.fill(own[0], command)
			group.set("hooks", []any{own[0]})
			setMatcher(group, want.matcher)

			placed = true
		case len(foreign) == 0:
			continue
		default:
			group.set("hooks", foreign)
		}

		merged = append(merged, group)
	}

	if !placed {
		merged = append(merged, want.group(command))
	}

	return merged, nil
}

// withoutOwn drops the handlers init wrote from an event's matcher groups,
// and the groups that held nothing else.
func withoutOwn(groups []any, command string) ([]any, error) {
	kept := make([]any, 0, len(groups))

	for _, item := range groups {
		group, ok := item.(*object)
		if !ok {
			return nil, fmt.Errorf("%w: a matcher group is not an object", ErrSettingsShape)
		}

		own, foreign := splitHandlers(group, command)

		switch {
		case len(own) == 0:
		case len(foreign) == 0:
			continue
		default:
			group.set("hooks", foreign)
		}

		kept = append(kept, group)
	}

	return kept, nil
}

// splitHandlers separates a matcher group's handlers into the ones init
// wrote and everything else, which is left untouched.
func splitHandlers(group *object, command string) ([]*object, []any) {
	handlers, ok := group.values["hooks"].([]any)
	if !ok {
		return nil, nil
	}

	var (
		own     []*object
		foreign []any
	)

	for _, handler := range handlers {
		if obj, ok := handler.(*object); ok && isOwn(obj, command) {
			own = append(own, obj)

			continue
		}

		foreign = append(foreign, handler)
	}

	return own, foreign
}

// isOwn recognizes a handler init wrote: a command hook that runs
// `<bin> hook`, where bin is the binary being installed now or one whose
// file name starts with "wavez". A binary pinned under any other name is
// not recognized once --bin changes, and would be left beside the new one.
func isOwn(handler *object, command string) bool {
	if kind, ok := handler.values["type"].(string); !ok || kind != "command" {
		return false
	}

	existing, ok := handler.values["command"].(string)
	if !ok {
		return false
	}

	if existing == command {
		return true
	}

	bin, ok := strings.CutSuffix(strings.TrimSpace(existing), hookArg)
	if !ok {
		return false
	}

	return strings.HasPrefix(filepath.Base(strings.Trim(bin, `'"`)), defaultBin)
}

func setMatcher(group *object, matcher string) {
	if matcher == "" {
		group.remove("matcher")

		return
	}

	group.set("matcher", matcher)
}

// fill makes handler run command, and gives it the entry's timeout and
// status message where it has none of its own.
func (e entry) fill(handler *object, command string) {
	handler.set("command", command)

	if _, has := handler.get("timeout"); !has {
		handler.set("timeout", json.Number(strconv.Itoa(e.timeout)))
	}

	if _, has := handler.get("statusMessage"); !has && e.status != "" {
		handler.set("statusMessage", e.status)
	}
}

func (e entry) group(command string) *object {
	handler := newObject()
	handler.set("type", "command")
	e.fill(handler, command)

	group := newObject()
	if e.matcher != "" {
		group.set("matcher", e.matcher)
	}

	group.set("hooks", []any{handler})

	return group
}
