package tui

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes CSI SGR escape sequences so the text can be copied
// cleanly to the clipboard.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// wrapANSI hard-wraps ANSI-styled text at the given rune-count width.
// CSI escape sequences (ESC [ ... m) pass through without counting toward
// column width, so lipgloss-colored content wraps at the visual boundary.
// Handles pre-existing newlines by resetting the column counter.
func wrapANSI(s string, width int) string {
	if width <= 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	col := 0
	i := 0
	for i < len(s) {
		c := s[i]
		// ANSI CSI: ESC [ ... m — emit verbatim, do not count width.
		if c == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				b.WriteString(s[i : j+1])
				i = j + 1
				continue
			}
		}
		if c == '\n' {
			b.WriteByte('\n')
			col = 0
			i++
			continue
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		if col >= width {
			b.WriteByte('\n')
			col = 0
		}
		b.WriteRune(r)
		i += sz
		col++
	}
	return b.String()
}