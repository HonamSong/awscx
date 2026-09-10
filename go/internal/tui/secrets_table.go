package tui

import (
	"fmt"
	"strings"
	"time"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

func secretsColWidths(tableWidth int) (int, int, int, int) {
	changedW, rotW, kmsW := 20, 20, 24
	nameW := tableWidth - idxColW - changedW - rotW - kmsW
	if nameW < 20 {
		nameW = 20
	}
	return nameW, changedW, rotW, kmsW
}

func fmtTimeShort(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// kmsShort trims KMS key id / arn to something readable.
func kmsShort(k string) string {
	if k == "" {
		return "-"
	}
	// arn:aws:kms:region:acct:key/UUID → key/UUID
	if i := strings.Index(k, ":key/"); i >= 0 {
		return "key/" + k[i+5:]
	}
	// alias/name or bare id — pass through
	return k
}

func secretCells(s awsx.SecretRow, widths [4]int, selected bool) [4]string {
	changed := fmtTimeShort(s.LastChanged)
	rotated := fmtTimeShort(s.LastRotated)
	kms := kmsShort(s.KMSKeyID)
	if selected {
		return [4]string{
			pad(s.Name, widths[0]),
			pad(changed, widths[1]),
			pad(rotated, widths[2]),
			pad(kms, widths[3]),
		}
	}
	rotCell := pad(dimStyle.Render(rotated), widths[2])
	if s.RotationEnabled {
		rotCell = pad(cyanStyle.Render(rotated), widths[2])
	}
	kmsCell := pad(dimStyle.Render(kms), widths[3])
	if s.KMSKeyID != "" {
		kmsCell = pad(cyanStyle.Render(kms), widths[3])
	}
	return [4]string{
		pad(s.Name, widths[0]),
		pad(changed, widths[1]),
		rotCell,
		kmsCell,
	}
}

func renderSecretsList(rows []awsx.SecretRow, cursor, tableWidth int) string {
	nameW, changedW, rotW, kmsW := secretsColWidths(tableWidth)
	widths := [4]int{nameW, changedW, rotW, kmsW}

	header := svcHeaderStyle.Render(
		pad("#", idxColW) +
			pad("NAME", widths[0]) +
			pad("LAST CHANGED", widths[1]) +
			pad("LAST ROTATED", widths[2]) +
			pad("KMS KEY", widths[3]))

	total := idxColW + nameW + changedW + rotW + kmsW
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(dividerLine(total))
	b.WriteByte('\n')
	for i, s := range rows {
		idx := fmt.Sprintf("%d", i+1)
		cells := secretCells(s, widths, i == cursor)
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