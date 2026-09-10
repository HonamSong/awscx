package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

// Custom EC2 list renderer (same reason as services_table.go: bubbles/table
// v1.0.0 corrupts ANSI-colored cells via non-ANSI-aware runewidth.Truncate).

func ec2ColWidths(tableWidth int) (int, int, int, int, int, int, int, int) {
	idW, typeW, stateW, ipW, pubW, ssmW, userW := 21, 16, 12, 17, 17, 14, 14
	nameW := tableWidth - idxColW - idW - typeW - stateW - ipW - pubW - ssmW - userW
	if nameW < 12 {
		nameW = 12
	}
	return idW, nameW, typeW, stateW, ipW, pubW, ssmW, userW
}

// osUserOrDash returns the OS default user or "-" when unknown.
func osUserOrDash(u string) string {
	if u == "" {
		return "-"
	}
	return u
}

// ec2StateStyle colors an EC2 lifecycle state.
func ec2StateStyle(state string) lipgloss.Style {
	switch strings.ToLower(state) {
	case "running":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	case "pending", "stopping", "shutting-down":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	case "stopped":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	case "terminated":
		return dimStyle
	}
	return lipgloss.NewStyle()
}

// ec2SSMStyle colors SSM PingStatus.
func ec2SSMStyle(status string) lipgloss.Style {
	switch strings.ToLower(status) {
	case "online":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	case "connectionlost":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	case "":
		return dimStyle
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
}

// ec2Cells returns 8 rendered cells for one row. Selected rows use plain
// text so the selection background can paint cleanly.
func ec2Cells(i awsx.EC2Instance, widths [8]int, selected bool) [8]string {
	ssm := i.SSM
	if ssm == "" {
		ssm = "-"
	}
	pub := i.PublicIP
	if pub == "" {
		pub = "-"
	}
	user := osUserOrDash(i.OSUser)
	if selected {
		return [8]string{
			pad(i.ID, widths[0]),
			pad(i.Name, widths[1]),
			pad(i.Type, widths[2]),
			pad(i.State, widths[3]),
			pad(i.IP, widths[4]),
			pad(pub, widths[5]),
			pad(ssm, widths[6]),
			pad(user, widths[7]),
		}
	}
	pubCell := pad(dimStyle.Render(pub), widths[5])
	if i.PublicIP != "" {
		pubCell = pad(cyanStyle.Render(i.PublicIP), widths[5])
	}
	userCell := pad(dimStyle.Render(user), widths[7])
	if i.OSUser != "" {
		userCell = pad(cyanStyle.Render(user), widths[7])
	}
	return [8]string{
		pad(i.ID, widths[0]),
		pad(i.Name, widths[1]),
		pad(i.Type, widths[2]),
		pad(ec2StateStyle(i.State).Render(i.State), widths[3]),
		pad(i.IP, widths[4]),
		pubCell,
		pad(ec2SSMStyle(i.SSM).Render(ssm), widths[6]),
		userCell,
	}
}

// renderEC2List produces header + N row lines with cursor row highlighted.
func renderEC2List(rows []awsx.EC2Instance, cursor, tableWidth int) string {
	idW, nameW, typeW, stateW, ipW, pubW, ssmW, userW := ec2ColWidths(tableWidth)
	widths := [8]int{idW, nameW, typeW, stateW, ipW, pubW, ssmW, userW}

	header := svcHeaderStyle.Render(
		pad("#", idxColW) +
			pad("ID", widths[0]) +
			pad("NAME", widths[1]) +
			pad("TYPE", widths[2]) +
			pad("STATE", widths[3]) +
			pad("IP", widths[4]) +
			pad("PUB IP", widths[5]) +
			pad("SSM", widths[6]) +
			pad("USER", widths[7]))

	total := idxColW + idW + nameW + typeW + stateW + ipW + pubW + ssmW + userW
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dividerLine(total))
	b.WriteByte('\n')
	for i, inst := range rows {
		idx := fmt.Sprintf("%d", i+1)
		cells := ec2Cells(inst, widths, i == cursor)
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