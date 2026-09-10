package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

// Custom renderer for the services list. Bypasses bubbles/table because
// v1.0.0 uses runewidth.Truncate on cell values which corrupts ANSI escape
// sequences (colors disappear + text vanishes). lipgloss.NewStyle().Width()
// is ANSI-aware and safe.

var (
	// k9s-스타일: 청록 볼드 헤더 + 밝은 청록 배경 선택.
	svcHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("51"))
	svcSelStyle    = lipgloss.NewStyle().
			Background(lipgloss.Color("39")).
			Foreground(lipgloss.Color("232")).
			Bold(true)
	svcDividerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	svcIdxStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

// dividerLine returns a horizontal rule of the given width.
func dividerLine(width int) string {
	if width < 1 {
		return ""
	}
	return svcDividerStyle.Render(strings.Repeat("─", width))
}

// serviceColWidths returns (name, status, exec, tasks, lb, scale) widths
// that sum to tableWidth. name absorbs the leftover.
func serviceColWidths(tableWidth int) (int, int, int, int, int, int) {
	statusW, execW, tasksW, lbW, scaleW := 10, 5, 10, 9, 9
	nameW := tableWidth - idxColW - statusW - execW - tasksW - lbW - scaleW
	if nameW < 15 {
		nameW = 15
	}
	return nameW, statusW, execW, tasksW, lbW, scaleW
}

// pad renders s constrained to width w, ANSI-aware.
func pad(s string, w int) string {
	return lipgloss.NewStyle().Width(w).Render(s)
}

// serviceCells returns the 6 rendered cell strings for one row. If selected,
// cells are plain text (no per-cell ANSI) so the selection background can
// paint the whole line cleanly.
func serviceCells(s awsx.ServiceRow, widths [6]int, selected bool) [6]string {
	statusTxt := orDash(s.Status)
	execTxt := "OFF"
	if s.ExecOn {
		execTxt = "ON"
	}
	tasksTxt := fmt.Sprintf("%d/%d", s.Running, s.Desired)
	lbTxt := "-"
	if s.HasLB {
		lbTxt = fmt.Sprintf("%d/%d", s.LBHealthy, s.LBTotal)
	}
	scaleTxt := "고정"
	if s.Min != nil && s.Max != nil {
		scaleTxt = fmt.Sprintf("%d~%d", *s.Min, *s.Max)
	}

	if selected {
		return [6]string{
			pad(s.Name, widths[0]),
			pad(statusTxt, widths[1]),
			pad(execTxt, widths[2]),
			pad(tasksTxt, widths[3]),
			pad(lbTxt, widths[4]),
			pad(scaleTxt, widths[5]),
		}
	}

	statusCell := statusStyle(s.Status).Render(statusTxt)
	var execCell string
	if s.ExecOn {
		execCell = statusStyle("HEALTHY").Render("ON")
	} else {
		execCell = redStyle.Render("OFF")
	}
	var tasksCell string
	if s.Running == s.Desired && s.Desired > 0 {
		tasksCell = statusStyle("HEALTHY").Render(tasksTxt)
	} else {
		tasksCell = statusStyle("PENDING").Render(tasksTxt)
	}
	var lbCell string
	if !s.HasLB {
		lbCell = dimStyle.Render(lbTxt)
	} else if s.LBTotal > 0 && s.LBHealthy == s.LBTotal {
		lbCell = statusStyle("HEALTHY").Render(lbTxt)
	} else {
		lbCell = redStyle.Render(lbTxt)
	}
	var scaleCell string
	if s.Min != nil && s.Max != nil {
		scaleCell = cyanStyle.Render(scaleTxt)
	} else {
		scaleCell = dimStyle.Render(scaleTxt)
	}
	return [6]string{
		pad(s.Name, widths[0]),
		pad(statusCell, widths[1]),
		pad(execCell, widths[2]),
		pad(tasksCell, widths[3]),
		pad(lbCell, widths[4]),
		pad(scaleCell, widths[5]),
	}
}

// renderServicesList produces a header + N row lines for the given rows,
// with the cursor row highlighted.
func renderServicesList(rows []awsx.ServiceRow, cursor, tableWidth int) string {
	nameW, statusW, execW, tasksW, lbW, scaleW := serviceColWidths(tableWidth)
	widths := [6]int{nameW, statusW, execW, tasksW, lbW, scaleW}

	header := svcHeaderStyle.Render(
		pad("#", idxColW) +
			pad("SERVICE", widths[0]) +
			pad("STATUS", widths[1]) +
			pad("EXEC", widths[2]) +
			pad("TASKS", widths[3]) +
			pad("LB", widths[4]) +
			pad("SCALE", widths[5]))

	total := idxColW + nameW + statusW + execW + tasksW + lbW + scaleW
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dividerLine(total))
	b.WriteByte('\n')
	for i, s := range rows {
		idx := fmt.Sprintf("%d", i+1)
		cells := serviceCells(s, widths, i == cursor)
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