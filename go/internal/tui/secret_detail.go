package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

type secretDetailLoadedMsg struct {
	detail *awsx.SecretDetail
}

type secretValueLoadedMsg struct {
	name  string
	value string
	err   error
}

func fetchSecretDetail(c *awsx.Clients, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		d, err := awsx.DescribeSecret(ctx, c.Secrets, id)
		if err != nil {
			return errMsg{fmt.Errorf("describe secret: %w", err)}
		}
		return secretDetailLoadedMsg{detail: d}
	}
}

func fetchSecretValue(c *awsx.Clients, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		v, err := awsx.GetSecretValue(ctx, c.Secrets, id)
		return secretValueLoadedMsg{name: id, value: v, err: err}
	}
}

func renderSecretDetail(d *awsx.SecretDetail, revealed bool, value string, valueErr error) string {
	if d == nil {
		return dimStyle.Render("(no data)")
	}
	var b strings.Builder

	b.WriteString(dimStyle.Render("Name          : "))
	b.WriteString(boldStyle.Render(d.Name) + "\n")
	b.WriteString(dimStyle.Render("ARN           : "))
	b.WriteString(dimStyle.Render(d.ARN) + "\n")
	if d.Description != "" {
		b.WriteString(dimStyle.Render("Description   : "))
		b.WriteString(d.Description + "\n")
	}
	b.WriteString(dimStyle.Render("KMS Key       : "))
	if d.KMSKeyID == "" {
		b.WriteString(dimStyle.Render("(default aws/secretsmanager)"))
	} else {
		b.WriteString(cyanStyle.Render(kmsShort(d.KMSKeyID)))
	}
	b.WriteString("\n")

	b.WriteString(dimStyle.Render("Created       : ") + fmtTimeShort(d.CreatedDate) + "\n")
	b.WriteString(dimStyle.Render("Last Changed  : ") + fmtTimeShort(d.LastChangedDate) + "\n")
	b.WriteString(dimStyle.Render("Last Rotated  : ") + fmtTimeShort(d.LastRotatedDate) + "\n")

	b.WriteString(dimStyle.Render("Rotation      : "))
	if d.RotationEnabled {
		b.WriteString(statusStyle("HEALTHY").Render("enabled"))
		if d.RotationRules != "" {
			b.WriteString("  " + dimStyle.Render(d.RotationRules))
		}
		if !d.NextRotationDate.IsZero() {
			b.WriteString("  " + dimStyle.Render("next=") + fmtTimeShort(d.NextRotationDate))
		}
	} else {
		b.WriteString(dimStyle.Render("disabled"))
	}
	b.WriteString("\n")

	// Versions
	b.WriteString("\n" + sectStyle.Render(fmt.Sprintf("Versions (%d):", len(d.Versions))) + "\n")
	if len(d.Versions) == 0 {
		b.WriteString(dimStyle.Render("  없음\n"))
	} else {
		ids := make([]string, 0, len(d.Versions))
		for id := range d.Versions {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			stages := d.Versions[id]
			shortID := id
			if len(shortID) > 8 {
				shortID = shortID[:8] + "…"
			}
			b.WriteString("  - " + shortID + "  ")
			for i, st := range stages {
				if i > 0 {
					b.WriteString(" ")
				}
				b.WriteString(cyanStyle.Render("[" + st + "]"))
			}
			b.WriteString("\n")
		}
	}

	// Tags
	if len(d.Tags) > 0 {
		b.WriteString("\n" + sectStyle.Render(fmt.Sprintf("Tags (%d):", len(d.Tags))) + "\n")
		for _, t := range d.Tags {
			b.WriteString("  " + dimStyle.Render(t.Key+"=") + t.Value + "\n")
		}
	}

	// Value
	b.WriteString("\n" + sectStyle.Render("Value:") + "\n")
	if valueErr != nil {
		b.WriteString(redStyle.Render(fmt.Sprintf("  조회 실패: %v\n", valueErr)))
	} else if !revealed {
		b.WriteString(dimStyle.Render("  (숨김 — v 눌러서 조회 · 화면·터미널 히스토리에 남을 수 있음)\n"))
	} else {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).
			Render("  ⚠ 노출됨 — 표시 후 터미널 히스토리에 남을 수 있음. 필요시 히스토리 삭제.") + "\n\n")
		// value 자체는 색 없이, 여러 줄이면 그대로 출력
		for _, line := range strings.Split(value, "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}