package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// copyToClipboard writes text to the system clipboard via the first available
// tool (pbcopy > wl-copy > xclip). If none is found, writes to
// ./awscx_output.txt as a fallback (matches python action_copy behavior).
// Returns a user-facing status message on success.
func copyToClipboard(text string) (string, error) {
	lines := strings.Count(text, "\n") + 1
	tools := []struct {
		name string
		args []string
	}{
		{"pbcopy", nil},
		{"wl-copy", nil},
		{"xclip", []string{"-selection", "clipboard"}},
	}
	for _, t := range tools {
		if _, err := exec.LookPath(t.name); err != nil {
			continue
		}
		cmd := exec.Command(t.name, t.args...)
		cmd.Stdin = bytes.NewBufferString(text)
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("%s: %w", t.name, err)
		}
		return fmt.Sprintf("클립보드 복사됨 (%d줄, %s)", lines, t.name), nil
	}
	path, err := filepath.Abs("awscx_output.txt")
	if err != nil {
		path = "awscx_output.txt"
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("클립보드 도구 없음 → 파일 저장: %s (%d줄)", path, lines), nil
}