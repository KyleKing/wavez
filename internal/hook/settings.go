package hook

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

const newFileMode = 0o644

// mergeSettings returns settings.json with wavez's five hook entries in
// it: PostToolUse on the edit tools, Stop, SessionStart, UserPromptSubmit,
// and PreToolUse on the shell tool. The UserPromptSubmit entry is installed
// whether or not the project has the route hint on, and the handler does
// nothing on that event until it is, so turning the hint on in .wavez.toml
// needs no second init. Every key and hook that is not wavez's own is kept as it was, in its
// place; an entry wavez installed earlier is updated where it stands, and
// surplus copies of it are removed. A nil current means there is no file.
// When the settings already say what init wants, current comes back
// byte for byte.
func mergeSettings(current []byte, command string) ([]byte, error) {
	doc := newObject()

	if len(bytes.TrimSpace(current)) > 0 {
		parsed, err := decodeJSON(current)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrSettingsShape, err)
		}

		var ok bool
		if doc, ok = parsed.(*object); !ok {
			return nil, fmt.Errorf("%w: the top level is not an object", ErrSettingsShape)
		}
	}

	before, err := encodeJSON(doc)
	if err != nil {
		return nil, err
	}

	if err := mergeHooks(doc, command); err != nil {
		return nil, err
	}

	after, err := encodeJSON(doc)
	if err != nil {
		return nil, err
	}

	// Nothing to change means the file's own formatting stays too.
	if current != nil && bytes.Equal(before, after) {
		return current, nil
	}

	return after, nil
}

func mergeHooks(doc *object, command string) error {
	hooks := newObject()

	if existing, ok := doc.get("hooks"); ok {
		if hooks, ok = existing.(*object); !ok {
			return fmt.Errorf("%w: hooks is not an object", ErrSettingsShape)
		}
	}

	entries := []struct{ event, matcher string }{
		{eventPostToolUse, EditTools},
		{eventStop, ""},
		{eventSessionStart, ""},
		{eventUserPromptSubmit, ""},
		{eventPreToolUse, ShellTool},
	}

	for _, entry := range entries {
		groups := []any{}

		if existing, ok := hooks.get(entry.event); ok {
			if groups, ok = existing.([]any); !ok {
				return fmt.Errorf("%w: hooks.%s is not an array", ErrSettingsShape, entry.event)
			}
		}

		merged, err := mergeEvent(groups, entry.matcher, command)
		if err != nil {
			return fmt.Errorf("hooks.%s: %w", entry.event, err)
		}

		hooks.set(entry.event, merged)
	}

	doc.set("hooks", hooks)

	return nil
}

// mergeEvent leaves exactly one wavez handler among an event's matcher
// groups. The first group that holds nothing but a wavez handler is updated
// in place, which keeps a timeout a person added to it; any other wavez
// handler is removed, and a group emptied that way goes with it.
func mergeEvent(groups []any, matcher, command string) ([]any, error) {
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
			own[0].set("command", command)
			group.set("hooks", []any{own[0]})
			setMatcher(group, matcher)

			placed = true
		case len(foreign) == 0:
			continue
		default:
			group.set("hooks", foreign)
		}

		merged = append(merged, group)
	}

	if !placed {
		merged = append(merged, ownGroup(matcher, command))
	}

	return merged, nil
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

func ownGroup(matcher, command string) *object {
	handler := newObject()
	handler.set("type", "command")
	handler.set("command", command)

	group := newObject()
	if matcher != "" {
		group.set("matcher", matcher)
	}

	group.set("hooks", []any{handler})

	return group
}
