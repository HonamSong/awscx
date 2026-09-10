package tui

import (
	"fmt"
	"strings"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

func recordColWidths(tableWidth int) (int, int, int, int) {
	typeW, ttlW := 8, 8
	valueMin := 30
	nameW := tableWidth - idxColW - typeW - ttlW - valueMin
	if nameW < 20 {
		nameW = 20
	}
	valueW := tableWidth - idxColW - nameW - typeW - ttlW
	if valueW < 10 {
		valueW = 10
	}
	return nameW, typeW, ttlW, valueW
}

func recordCells(r awsx.R53RecordRow, widths [4]int, selected bool) [4]string {
	var value string
	if r.AliasDNS != "" {
		value = "ALIAS → " + r.AliasDNS
	} else {
		value = strings.Join(r.Values, ", ")
	}
	ttl := "-"
	if r.TTL > 0 {
		ttl = fmt.Sprintf("%d", r.TTL)
	}
	if selected {
		return [4]string{
			pad(r.Name, widths[0]),
			pad(r.Type, widths[1]),
			pad(ttl, widths[2]),
			pad(value, widths[3]),
		}
	}
	typeStyle := cyanStyle
	// well-known record type coloring
	switch r.Type {
	case "A", "AAAA":
		typeStyle = statusStyle("HEALTHY")
	case "CNAME":
		typeStyle = cyanStyle
	case "MX", "NS", "SOA":
		typeStyle = statusStyle("PENDING")
	case "TXT":
		typeStyle = dimStyle
	}
	valueCell := pad(value, widths[3])
	if r.AliasDNS != "" {
		valueCell = pad(cyanStyle.Render(value), widths[3])
	}
	return [4]string{
		pad(r.Name, widths[0]),
		pad(typeStyle.Render(r.Type), widths[1]),
		pad(dimStyle.Render(ttl), widths[2]),
		valueCell,
	}
}

func renderR53RecordList(rows []awsx.R53RecordRow, cursor, tableWidth int) string {
	nameW, typeW, ttlW, valueW := recordColWidths(tableWidth)
	widths := [4]int{nameW, typeW, ttlW, valueW}

	header := svcHeaderStyle.Render(
		pad("#", idxColW) +
			pad("NAME", widths[0]) +
			pad("TYPE", widths[1]) +
			pad("TTL", widths[2]) +
			pad("VALUE", widths[3]))

	total := idxColW + nameW + typeW + ttlW + valueW
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dividerLine(total))
	b.WriteByte('\n')
	for i, r := range rows {
		idx := fmt.Sprintf("%d", i+1)
		cells := recordCells(r, widths, i == cursor)
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