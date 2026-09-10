package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

// Custom profile picker renderer. Adds TYPE and TOKEN columns with color
// (SSO/assume-role expiration status).

const idxColW = 5

func profileColWidths(tableWidth int) (int, int, int) {
	typeW, tokenW := 13, 18
	nameW := tableWidth - idxColW - typeW - tokenW
	if nameW < 12 {
		nameW = 12
	}
	return nameW, typeW, tokenW
}

// tokenStatus renders a colored token status cell + returns its plain text
// (used when the row is selected so selection bg paints cleanly).
func tokenStatus(pt awsx.ProfileToken) (colored, plain string) {
	if pt.Kind == "static" {
		return dimStyle.Render("-"), "-"
	}
	if !pt.HasCache {
		return dimStyle.Render("no cache"), "no cache"
	}
	remaining := time.Until(pt.ExpiresAt)
	if remaining <= 0 {
		return redStyle.Render("EXPIRED"), "EXPIRED"
	}
	txt := fmtDuration(remaining)
	if remaining < 15*time.Minute {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(txt), txt
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(txt), txt
}

func fmtDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) - h*60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func kindLabel(kind string) string {
	switch kind {
	case "sso":
		return "sso"
	case "assume-role":
		return "assume-role"
	case "static":
		return "static"
	}
	return kind
}

func profileCells(pt awsx.ProfileToken, widths [3]int, selected bool) [3]string {
	kind := kindLabel(pt.Kind)
	tokenColor, tokenPlain := tokenStatus(pt)
	if selected {
		return [3]string{
			pad(pt.Name, widths[0]),
			pad(kind, widths[1]),
			pad(tokenPlain, widths[2]),
		}
	}
	return [3]string{
		pad(pt.Name, widths[0]),
		pad(dimStyle.Render(kind), widths[1]),
		pad(tokenColor, widths[2]),
	}
}

func renderProfileList(rows []awsx.ProfileToken, cursor, tableWidth int) string {
	nameW, typeW, tokenW := profileColWidths(tableWidth)
	widths := [3]int{nameW, typeW, tokenW}
	header := svcHeaderStyle.Render(
		pad("#", idxColW) +
			pad("PROFILE", widths[0]) +
			pad("TYPE", widths[1]) +
			pad("TOKEN", widths[2]))
	total := idxColW + nameW + typeW + tokenW
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dividerLine(total))
	b.WriteByte('\n')
	for i, pt := range rows {
		idx := fmt.Sprintf("%d", i+1)
		cells := profileCells(pt, widths, i == cursor)
		var line string
		if i == cursor {
			line = svcSelStyle.Render(pad(idx, idxColW) + strings.Join(cells[:], ""))
		} else {
			line = pad(svcIdxStyle.Render(idx), idxColW) + strings.Join(cells[:], "")
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}