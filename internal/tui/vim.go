package tui

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// vimMode is the composer's editing mode. There are two: the composer has
// no visual or command mode, and `:` reaches the palette instead.
type vimMode int

const (
	modeNormal vimMode = iota
	modeInsert
)

func (v vimMode) tag() string {
	if v == modeInsert {
		return "INS"
	}

	return "NOR"
}

// charClass groups runes the way vim's word motions do. A motion stops
// wherever the class changes, so `w` lands on `(` in `f(x)` rather than
// skipping to the next space-separated token.
type charClass int

const (
	classSpace charClass = iota
	classWord
	classPunct
)

func classOf(r rune) charClass {
	switch {
	case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return classWord
	case unicode.IsSpace(r):
		return classSpace
	default:
		return classPunct
	}
}

// pos is a cursor position: a row in the buffer and a rune offset into it.
type pos struct{ row, col int }

// undoState is one buffer snapshot. `u` restores the most recent one.
type undoState struct {
	lines [][]rune
	cur   pos
}

// maxUndo bounds the snapshot stack; a prompt composer has no use for a
// deeper history than this and an unbounded stack grows with the session.
const maxUndo = 100

// vimInput is the thread composer: a modal editor with vim's normal and
// insert modes and nothing else. It replaces bubbles/v2 textinput on the
// thread screen because neither bubbles nor any Bubble Tea v2 library
// offers modal editing (vimtea, the one Go component that does, is still
// on Bubble Tea v1).
//
// Focus, not mode, decides whether a letter is a verb or a character:
// every key belongs to this editor while the input panel holds focus, and
// the thread screen's verbs work from the transcript and diff panels. Esc
// steps out one level at a time (insert to normal, normal to the transcript
// panel) and never quits.
type vimInput struct {
	placeholder string
	pending     string
	lines       [][]rune
	reg         [][]rune
	undo        []undoState
	cur         pos
	mode        vimMode
	width       int
	regLine     bool
	focused     bool
}

func newVimInput(placeholder string) vimInput {
	return vimInput{lines: [][]rune{{}}, placeholder: placeholder}
}

// Value returns the composed message, lines joined by newlines.
func (v vimInput) Value() string {
	out := make([]string, len(v.lines))
	for i := range v.lines {
		out[i] = string(v.lines[i])
	}

	return strings.Join(out, "\n")
}

// SetValue replaces the buffer and puts the cursor at the end.
func (v *vimInput) SetValue(s string) {
	parts := strings.Split(s, "\n")
	v.lines = make([][]rune, len(parts))

	for i, p := range parts {
		v.lines[i] = []rune(p)
	}

	v.cur = pos{len(v.lines) - 1, len(v.lines[len(v.lines)-1])}
	v.undo = nil
	v.pending = ""
	v.clamp()
}

// Reset empties the buffer, keeping the mode so a second message can be
// typed straight after the first.
func (v *vimInput) Reset() {
	v.lines = [][]rune{{}}
	v.cur = pos{}
	v.undo = nil
	v.pending = ""
}

// Focus gives the composer the keyboard and starts it in insert mode:
// moving focus here is the gesture for wanting to write.
func (v *vimInput) Focus() {
	v.focused = true
	v.mode = modeInsert
	v.clamp()
}

func (v *vimInput) Blur() {
	v.focused = false
	v.mode = modeNormal
	v.pending = ""
	v.clamp()
}

func (v *vimInput) SetWidth(w int) { v.width = max(w, 1) }

// leaveInsert drops to normal mode, moving the cursor left the way vim
// does so the character just typed stays under it.
func (v *vimInput) leaveInsert() {
	v.mode = modeNormal
	v.cur.col--
	v.pending = ""
	v.clamp()
}

func (v *vimInput) clamp() {
	v.cur.row = min(max(v.cur.row, 0), len(v.lines)-1)

	last := len(v.lines[v.cur.row])
	if v.mode == modeNormal && last > 0 {
		last--
	}

	v.cur.col = min(max(v.cur.col, 0), last)
}

func (v vimInput) line() []rune { return v.lines[v.cur.row] }

func (v *vimInput) snapshot() {
	cp := make([][]rune, len(v.lines))
	for i := range v.lines {
		cp[i] = append([]rune(nil), v.lines[i]...)
	}

	v.undo = append(v.undo, undoState{lines: cp, cur: v.cur})
	if len(v.undo) > maxUndo {
		v.undo = v.undo[len(v.undo)-maxUndo:]
	}
}

func (v *vimInput) restore() {
	if len(v.undo) == 0 {
		return
	}

	last := v.undo[len(v.undo)-1]
	v.undo = v.undo[:len(v.undo)-1]
	v.lines, v.cur = last.lines, last.cur
	v.clamp()
}

// handleKey applies one key press. Esc is not routed here: the model's
// Esc ladder owns leaving insert mode, so that one key stays in one place.
func (v *vimInput) handleKey(msg tea.KeyPressMsg, s string) {
	if v.mode == modeInsert {
		v.insertKey(msg, s)

		return
	}

	v.normalKey(s)
}

func (v *vimInput) insertKey(msg tea.KeyPressMsg, s string) {
	switch s {
	case "backspace":
		v.backspace()
	case "delete":
		v.deleteRune()
	case keyEnter:
		v.splitLine()
	case "ctrl+w":
		v.deleteWordBefore()
	case "left", "right", keyUp, keyDown, keyHome, "end":
		v.motion(s)
	default:
		if msg.Mod&tea.ModCtrl != 0 || msg.Text == "" {
			return
		}

		v.insertText(msg.Text)
	}
}

func (v *vimInput) normalKey(s string) {
	if v.pending != "" {
		v.operator(s)

		return
	}

	switch s {
	case "d", "c", "g":
		v.pending = s
	case "i", "a", "I", "A", "o", "O":
		v.enterInsert(s)
	case "x":
		v.snapshot()
		v.cutSpan(v.cur.col, v.cur.col+1)
		v.clamp()
	case "D", "C":
		v.snapshot()
		v.cutSpan(v.cur.col, len(v.line()))

		if s == "C" {
			v.mode = modeInsert
		}

		v.clamp()
	case "p", "P":
		v.paste(s == "p")
	case "u":
		v.restore()
	default:
		v.motion(s)
	}
}

// operator completes a pending `d`, `c`, or `g` with the key that follows.
// A motion it does not cover drops the operator rather than guessing.
func (v *vimInput) operator(s string) {
	op := v.pending
	v.pending = ""

	if op == "g" {
		if s == "g" {
			v.cur = pos{}
			v.clamp()
		}

		return
	}

	if s == op {
		v.snapshot()
		v.cutLine(op == "c")

		if op == "c" {
			v.mode = modeInsert
		}

		v.clamp()

		return
	}

	// vim's cw changes to the end of the word rather than to the start of
	// the next one, which is the difference every vim user has in muscle
	// memory.
	if op == "c" && s == "w" {
		s = "e"
	}

	sp, ok := v.span(s)
	if !ok {
		return
	}

	v.snapshot()
	v.cutSpan(sp.from, sp.to)

	if op == "c" {
		v.mode = modeInsert
	}

	v.clamp()
}

// runeSpan is a half-open column range on one line.
type runeSpan struct{ from, to int }

// span resolves a motion into the range on the current line an operator
// applies to.
func (v vimInput) span(s string) (runeSpan, bool) {
	if s == "$" {
		return runeSpan{v.cur.col, len(v.line())}, true
	}

	tgt, found := v.target(s)
	if !found {
		return runeSpan{}, false
	}

	col := tgt.col

	switch {
	case tgt.row > v.cur.row:
		col = len(v.line())
	case tgt.row < v.cur.row:
		col = 0
	case s == "e":
		col++
	}

	if col < v.cur.col {
		return runeSpan{col, v.cur.col}, true
	}

	return runeSpan{v.cur.col, col}, true
}

func (v *vimInput) motion(s string) {
	if p, ok := v.target(s); ok {
		v.cur = p
		v.clamp()
	}
}

// target resolves a motion key to the position it lands on. A column past
// the end of a line is trimmed by clamp, so `$` may return one past the
// last rune.
func (v vimInput) target(s string) (pos, bool) {
	switch s {
	case "h", "left":
		return pos{v.cur.row, v.cur.col - 1}, true
	case "l", "right":
		return pos{v.cur.row, v.cur.col + 1}, true
	case keyJ, keyDown:
		return pos{v.cur.row + 1, v.cur.col}, true
	case keyK, keyUp:
		return pos{v.cur.row - 1, v.cur.col}, true
	case "0", keyHome:
		return pos{v.cur.row, 0}, true
	case "^":
		return pos{v.cur.row, firstNonBlank(v.line())}, true
	case "$", "end":
		return pos{v.cur.row, len(v.line())}, true
	case "w":
		return v.nextWordStart(), true
	case "b":
		return v.prevWordStart(), true
	case "e":
		return v.wordEnd(), true
	case "G":
		return pos{len(v.lines) - 1, 0}, true
	default:
		return pos{}, false
	}
}

func firstNonBlank(line []rune) int {
	for i, r := range line {
		if !unicode.IsSpace(r) {
			return i
		}
	}

	return 0
}

func (v *vimInput) enterInsert(s string) {
	v.snapshot()
	v.mode = modeInsert

	switch s {
	case "a":
		v.cur.col++
	case "I":
		v.cur.col = firstNonBlank(v.line())
	case "A":
		v.cur.col = len(v.line())
	case "o":
		v.openLine(v.cur.row + 1)
	case "O":
		v.openLine(v.cur.row)
	}

	v.clamp()
}

func (v *vimInput) openLine(row int) {
	rest := append([][]rune{{}}, v.lines[row:]...)
	v.lines = append(v.lines[:row:row], rest...)
	v.cur = pos{row, 0}
}

// cutSpan removes [from, to) from the current line into the register.
func (v *vimInput) cutSpan(from, to int) {
	line := v.line()
	from = min(max(from, 0), len(line))
	to = min(max(to, from), len(line))

	v.reg = [][]rune{append([]rune(nil), line[from:to]...)}
	v.regLine = false

	next := append([]rune(nil), line[:from]...)
	v.lines[v.cur.row] = append(next, line[to:]...)
	v.cur.col = from
}

// cutLine takes the whole line into the register. `cc` empties the line and
// keeps it, `dd` removes it.
func (v *vimInput) cutLine(keep bool) {
	row := v.cur.row
	v.reg = [][]rune{append([]rune(nil), v.lines[row]...)}
	v.regLine = true

	if keep {
		v.lines[row] = nil
		v.cur.col = 0

		return
	}

	v.lines = append(v.lines[:row], v.lines[row+1:]...)
	if len(v.lines) == 0 {
		v.lines = [][]rune{{}}
	}

	v.cur = pos{min(row, len(v.lines)-1), 0}
}

func (v *vimInput) paste(after bool) {
	if len(v.reg) == 0 {
		return
	}

	v.snapshot()

	if v.regLine {
		row := v.cur.row
		if after {
			row++
		}

		rest := append(copyLines(v.reg), v.lines[row:]...)
		v.lines = append(v.lines[:row:row], rest...)
		v.cur = pos{row, 0}
		v.clamp()

		return
	}

	line := v.line()

	at := v.cur.col
	if after && len(line) > 0 {
		at++
	}

	at = min(at, len(line))

	next := append([]rune(nil), line[:at]...)
	next = append(next, v.reg[0]...)
	v.lines[v.cur.row] = append(next, line[at:]...)
	v.cur.col = at + len(v.reg[0]) - 1
	v.clamp()
}

func copyLines(src [][]rune) [][]rune {
	out := make([][]rune, len(src))
	for i := range src {
		out[i] = append([]rune(nil), src[i]...)
	}

	return out
}

func (v *vimInput) insertText(text string) {
	line := v.line()
	at := min(v.cur.col, len(line))

	next := append([]rune(nil), line[:at]...)
	next = append(next, []rune(text)...)
	v.lines[v.cur.row] = append(next, line[at:]...)
	v.cur.col = at + len([]rune(text))
}

func (v *vimInput) backspace() {
	if v.cur.col > 0 {
		v.cutSpan(v.cur.col-1, v.cur.col)

		return
	}

	if v.cur.row == 0 {
		return
	}

	prev := v.lines[v.cur.row-1]
	v.cur = pos{v.cur.row - 1, len(prev)}
	v.lines[v.cur.row] = append(append([]rune(nil), prev...), v.lines[v.cur.row+1]...)
	v.lines = append(v.lines[:v.cur.row+1], v.lines[v.cur.row+2:]...)
}

func (v *vimInput) deleteRune() {
	if v.cur.col < len(v.line()) {
		v.cutSpan(v.cur.col, v.cur.col+1)
	}
}

func (v *vimInput) deleteWordBefore() {
	if v.cur.col == 0 {
		v.backspace()

		return
	}

	start := v.prevWordStart()
	if start.row != v.cur.row {
		start = pos{v.cur.row, 0}
	}

	v.cutSpan(start.col, v.cur.col)
}

func (v *vimInput) splitLine() {
	line := v.line()
	at := min(v.cur.col, len(line))

	head := append([]rune(nil), line[:at]...)
	tail := append([]rune(nil), line[at:]...)

	rest := append([][]rune{tail}, v.lines[v.cur.row+1:]...)
	v.lines = append(append(v.lines[:v.cur.row:v.cur.row], head), rest...)
	v.cur = pos{v.cur.row + 1, 0}
}

// wordBeforeCursor returns the word-class run before the cursor and the
// column it starts at.
func (v vimInput) wordBeforeCursor() (int, string) {
	line := v.line()
	start := v.cur.col

	for start > 0 && classOf(line[start-1]) == classWord {
		start--
	}

	return start, string(line[start:v.cur.col])
}

// replaceSpan splices text over [from, to) on the current line, splitting
// on newlines into new buffer lines, and leaves the cursor after it.
func (v *vimInput) replaceSpan(from, to int, text string) {
	line := v.line()
	from = min(max(from, 0), len(line))
	to = min(max(to, from), len(line))

	head := append([]rune(nil), line[:from]...)
	tail := append([]rune(nil), line[to:]...)

	parts := strings.Split(text, "\n")
	inserted := make([][]rune, len(parts))

	for i, p := range parts {
		inserted[i] = []rune(p)
	}

	inserted[0] = append(head, inserted[0]...)
	lastIdx := len(inserted) - 1
	cursorCol := len(inserted[lastIdx])
	inserted[lastIdx] = append(inserted[lastIdx], tail...)

	rest := append([][]rune(nil), v.lines[v.cur.row+1:]...)
	v.lines = append(v.lines[:v.cur.row:v.cur.row], inserted...)
	v.lines = append(v.lines, rest...)

	v.cur = pos{v.cur.row + lastIdx, cursorCol}
}

// at reports the rune at p, treating the end of a line as whitespace so a
// word motion crosses it the way vim does.
func (v vimInput) at(p pos) rune {
	line := v.lines[p.row]
	if p.col >= len(line) {
		return '\n'
	}

	return line[p.col]
}

func (v vimInput) forward(p pos) (pos, bool) {
	if p.col < len(v.lines[p.row]) {
		return pos{p.row, p.col + 1}, true
	}

	if p.row+1 < len(v.lines) {
		return pos{p.row + 1, 0}, true
	}

	return p, false
}

func (v vimInput) backward(p pos) (pos, bool) {
	if p.col > 0 {
		return pos{p.row, p.col - 1}, true
	}

	if p.row > 0 {
		return pos{p.row - 1, len(v.lines[p.row-1])}, true
	}

	return p, false
}

func (v vimInput) nextWordStart() pos {
	p, ok := v.cur, true
	for c := classOf(v.at(p)); ok && c != classSpace && classOf(v.at(p)) == c; {
		p, ok = v.forward(p)
	}

	for ok && classOf(v.at(p)) == classSpace {
		p, ok = v.forward(p)
	}

	return p
}

func (v vimInput) prevWordStart() pos {
	p, ok := v.backward(v.cur)
	for ok && classOf(v.at(p)) == classSpace {
		p, ok = v.backward(p)
	}

	c := classOf(v.at(p))

	for {
		prev, more := v.backward(p)
		if !more || classOf(v.at(prev)) != c {
			return p
		}

		p = prev
	}
}

func (v vimInput) wordEnd() pos {
	p, ok := v.forward(v.cur)
	for ok && classOf(v.at(p)) == classSpace {
		p, ok = v.forward(p)
	}

	c := classOf(v.at(p))

	for {
		next, more := v.forward(p)
		if !more || classOf(v.at(next)) != c {
			return p
		}

		p = next
	}
}

// inlineView renders the composer as the single row Thread view has for it:
// the mode tag, then the line the cursor is on.
func (v vimInput) inlineView(th theme) string {
	tag := v.tagText()

	return v.tagStyle(th).Render(tag) + " " + v.lineView(th, v.cur.row, max(v.width-len(tag)-1, 1))
}

// tagText names the mode, and the cursor's line out of the total once the
// buffer has more than one, since the inline row shows only one of them.
func (v vimInput) tagText() string {
	tag := v.mode.tag()
	if len(v.lines) > 1 {
		tag += " " + strconv.Itoa(v.cur.row+1) + "/" + strconv.Itoa(len(v.lines))
	}

	return tag
}

func (v vimInput) tagStyle(th theme) lipgloss.Style {
	if !v.focused {
		return th.fgMuted
	}

	if v.mode == modeInsert {
		return th.accent
	}

	return th.fgEmphasis
}

// lineView renders one buffer line, scrolled so the cursor stays visible.
func (v vimInput) lineView(th theme, row, width int) string {
	if v.empty() && !v.focused {
		return th.fgMuted.Render(truncate(v.placeholder, width))
	}

	line := v.lines[row]

	cursor := -1
	if v.focused && row == v.cur.row {
		cursor = v.cur.col
	}

	start := 0
	if cursor >= width {
		start = cursor - width + 1
	}

	visible := line[min(start, len(line)):min(start+width, len(line))]
	if cursor < 0 {
		return th.fgDefault.Render(string(visible))
	}

	rel := cursor - start
	if rel >= len(visible) {
		return th.fgDefault.Render(string(visible)) + th.cursor.Render(" ")
	}

	return th.fgDefault.Render(string(visible[:rel])) +
		th.cursor.Render(string(visible[rel])) +
		th.fgDefault.Render(string(visible[rel+1:]))
}

func (v vimInput) empty() bool { return len(v.lines) == 1 && len(v.lines[0]) == 0 }

// composeBody renders the fullscreen composer: the buffer windowed around
// the cursor, then vim's own status line.
func (v vimInput) composeBody(th theme, width, height int) []string {
	rows := max(height-1, 1)

	start := max(min(v.cur.row-rows/2, len(v.lines)-rows), 0)
	end := min(start+rows, len(v.lines))

	body := make([]string, 0, rows+1)
	for i := start; i < end; i++ {
		body = append(body, v.lineView(th, i, width))
	}

	for len(body) < rows {
		body = append(body, "")
	}

	return append(body, v.statusLine(th, width))
}

func (v vimInput) statusLine(th theme, width int) string {
	mode := "-- NORMAL --"
	if v.mode == modeInsert {
		mode = "-- INSERT --"
	}

	where := "ln " + strconv.Itoa(v.cur.row+1) + "/" + strconv.Itoa(len(v.lines)) + "  col " + strconv.Itoa(v.cur.col+1)

	return v.tagStyle(th).Render(truncate(mode, width)) + th.fgMuted.Render("  "+truncate(where, width))
}
