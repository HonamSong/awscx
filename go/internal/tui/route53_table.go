package tui

import (
	"fmt"
	"strings"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

func r53ColWidths(tableWidth int) (int, int, int, int, int) {
	idW, typeW, recW := 24, 10, 8
	commentMin := 20
	nameW := tableWidth - idxColW - idW - typeW - recW - commentMin
	if nameW < 20 {
		nameW = 20
	}
	commentW := tableWidth - idxColW - idW - nameW - typeW - recW
	if commentW < 10 {
		commentW = 10
	}
	return idW, nameW, typeW, recW, commentW
}

func r53Cells(z awsx.R53ZoneRow, widths [5]int, selected bool) [5]string {
	typ := "public"
	if z.Private {
		typ = "private"
	}
	recs := fmt.Sprintf("%d", z.RecordCount)
	comment := z.Comment
	if comment == "" {
		comment = "-"
	}
	if selected {
		return [5]string{
			pad(z.ID, widths[0]),
			pad(z.Name, widths[1]),
			pad(typ, widths[2]),
			pad(recs, widths[3]),
			pad(comment, widths[4]),
		}
	}
	typeCell := pad(cyanStyle.Render(typ), widths[2])
	if z.Private {
		typeCell = pad(statusStyle("PENDING").Render(typ), widths[2])
	}
	commentCell := pad(dimStyle.Render(comment), widths[4])
	if z.Comment != "" {
		commentCell = pad(comment, widths[4])
	}
	return [5]string{
		pad(z.ID, widths[0]),
		pad(z.Name, widths[1]),
		typeCell,
		pad(statusStyle("HEALTHY").Render(recs), widths[3]),
		commentCell,
	}
}

func renderR53List(rows []awsx.R53ZoneRow, cursor, tableWidth int) string {
	idW, nameW, typeW, recW, commentW := r53ColWidths(tableWidth)
	widths := [5]int{idW, nameW, typeW, recW, commentW}

	header := svcHeaderStyle.Render(
		pad("#", idxColW) +
			pad("ZONE ID", widths[0]) +
			pad("NAME", widths[1]) +
			pad("TYPE", widths[2]) +
			pad("RECS", widths[3]) +
			pad("COMMENT", widths[4]))

	total := idxColW + idW + nameW + typeW + recW + commentW
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dividerLine(total))
	b.WriteByte('\n')
	for i, z := range rows {
		idx := fmt.Sprintf("%d", i+1)
		cells := r53Cells(z, widths, i == cursor)
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