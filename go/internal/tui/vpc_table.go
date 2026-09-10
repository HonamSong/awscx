package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

func vpcColWidths(tableWidth int) (int, int, int, int, int) {
	idW, cidrW, stateW, defW := 24, 20, 12, 10
	nameW := tableWidth - idxColW - idW - cidrW - stateW - defW
	if nameW < 12 {
		nameW = 12
	}
	return idW, nameW, cidrW, stateW, defW
}

func vpcStateStyle(state string) lipgloss.Style {
	if strings.ToLower(state) == "available" {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
}

func vpcCells(v awsx.VPCRow, widths [5]int, selected bool) [5]string {
	def := "-"
	if v.IsDefault {
		def = "default"
	}
	if selected {
		return [5]string{
			pad(v.ID, widths[0]),
			pad(v.Name, widths[1]),
			pad(v.CIDR, widths[2]),
			pad(v.State, widths[3]),
			pad(def, widths[4]),
		}
	}
	defCell := pad(dimStyle.Render(def), widths[4])
	if v.IsDefault {
		defCell = pad(cyanStyle.Render(def), widths[4])
	}
	return [5]string{
		pad(v.ID, widths[0]),
		pad(v.Name, widths[1]),
		pad(cyanStyle.Render(v.CIDR), widths[2]),
		pad(vpcStateStyle(v.State).Render(v.State), widths[3]),
		defCell,
	}
}

func renderVPCList(rows []awsx.VPCRow, cursor, tableWidth int) string {
	idW, nameW, cidrW, stateW, defW := vpcColWidths(tableWidth)
	widths := [5]int{idW, nameW, cidrW, stateW, defW}

	header := svcHeaderStyle.Render(
		pad("#", idxColW) +
			pad("VPC ID", widths[0]) +
			pad("NAME", widths[1]) +
			pad("CIDR", widths[2]) +
			pad("STATE", widths[3]) +
			pad("FLAG", widths[4]))

	total := idxColW + idW + nameW + cidrW + stateW + defW
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dividerLine(total))
	b.WriteByte('\n')
	for i, v := range rows {
		idx := fmt.Sprintf("%d", i+1)
		cells := vpcCells(v, widths, i == cursor)
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