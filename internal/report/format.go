package report

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Sizes of the units a page prints.
const (
	hundred    = 100
	groupWidth = 3
	shortID    = 8
	dateLayout = "2006-01-02 15:04 UTC"
)

// number prints n with a comma between each group of three digits.
func number(n int) string {
	digits := strconv.Itoa(n)
	if n < 0 {
		return "-" + number(-n)
	}

	var b strings.Builder

	for i, r := range digits {
		if i > 0 && (len(digits)-i)%groupWidth == 0 {
			b.WriteByte(',')
		}

		b.WriteRune(r)
	}

	return b.String()
}

// percent prints part of whole as a whole percentage, and NotRecorded when
// there is no whole to take a part of.
func percent(part, whole int) string {
	if whole <= 0 {
		return NotRecorded
	}

	return strconv.Itoa((part*hundred+whole/2)/whole) + "%"
}

// span prints a duration to the precision a person reads it at.
func span(d time.Duration) string {
	switch {
	case d <= 0:
		return NotRecorded
	case d < time.Second:
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	case d < time.Minute:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%int(time.Minute/time.Second))
	default:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%int(time.Hour/time.Minute))
	}
}

// offset prints how far into the session a moment fell.
func offset(start, at time.Time) string {
	if start.IsZero() || at.IsZero() {
		return NotRecorded
	}

	d := at.Sub(start).Round(time.Second)
	total := int(d.Seconds())
	perMinute := int(time.Minute / time.Second)
	perHour := int(time.Hour / time.Second)

	if total >= perHour {
		return fmt.Sprintf("%d:%02d:%02d", total/perHour, total%perHour/perMinute, total%perMinute)
	}

	return fmt.Sprintf("%d:%02d", total/perMinute, total%perMinute)
}

// size prints a count of bytes.
func size(n int) string {
	if n == 1 {
		return "1 byte"
	}

	return number(n) + " bytes"
}

// dollars prints a cost, to the cent once it is worth a dollar.
func dollars(usd float64) string {
	if usd >= 1 {
		return "$" + strconv.FormatFloat(usd, 'f', 2, 64)
	}

	return "$" + strconv.FormatFloat(usd, 'f', 3, 64)
}

// plural prints a count with its noun, adding an s unless n is one.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return number(n) + " " + noun + "s"
}

// list joins names the way a sentence does: "a", "a and b", "a, b, and c".
func list(names []string) string {
	const pair = 2

	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case pair:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}

// short cuts a session id to the part a person reads.
func short(id string) string {
	if len(id) <= shortID {
		return id
	}

	return id[:shortID]
}

// measured prints a duration that was recorded, where zero is a real answer.
func measured(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}

	return span(d)
}
