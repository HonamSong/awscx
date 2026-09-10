package tui

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

// Log-viewer messages.

type logsLoadedMsg struct {
	lines     []string
	cfg       awsx.AwsLogs
	container string
	lastTs    int64
}

type logsTailMsg struct {
	lines  []string
	lastTs int64
}

type logsTickMsg struct{}

// fetchLogs picks the first running task + first awslogs-driver container of
// the given service and fetches the last hour of events.
func fetchLogs(c *awsx.Clients, cluster, service, fallbackRegion string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()

		tasks, err := awsx.ListRunningTasks(ctx, c.ECS, cluster, service)
		if err != nil {
			return errMsg{fmt.Errorf("list tasks: %w", err)}
		}
		if len(tasks) == 0 {
			return errMsg{fmt.Errorf("실행 중인 task 가 없습니다")}
		}
		task := tasks[0]

		td, err := awsx.GetTaskDefinition(ctx, c.ECS, awssdk.ToString(task.TaskDefinitionArn))
		if err != nil {
			return errMsg{fmt.Errorf("task def: %w", err)}
		}

		var cfg *awsx.AwsLogs
		var cname string
		for _, cd := range td.ContainerDefinitions {
			if lg := awsx.AwsLogsConfig(cd); lg != nil && lg.Group != "" {
				cfg = lg
				cname = awssdk.ToString(cd.Name)
				break
			}
		}
		if cfg == nil {
			return errMsg{fmt.Errorf("awslogs 로그 드라이버가 설정된 컨테이너가 없습니다")}
		}
		_ = fallbackRegion // Logs client already targets the session region.

		prefix := ""
		if cfg.Prefix != "" {
			prefix = cfg.Prefix + "/" + cname
		}
		startMs := time.Now().Add(-time.Hour).UnixMilli()
		events, err := awsx.FilterLogEvents(ctx, c.Logs, cfg.Group, prefix, startMs, 300)
		if err != nil {
			return errMsg{fmt.Errorf("filter log events: %w", err)}
		}
		sort.Slice(events, func(i, j int) bool { return events[i].Timestamp < events[j].Timestamp })
		if len(events) > 200 {
			events = events[len(events)-200:]
		}
		lines := make([]string, len(events))
		var lastTs int64
		for i, e := range events {
			lines[i] = fmtLogEvent(e)
			if e.Timestamp > lastTs {
				lastTs = e.Timestamp
			}
		}
		return logsLoadedMsg{lines: lines, cfg: *cfg, container: cname, lastTs: lastTs}
	}
}

// pollLogs fetches events with timestamp > lastTs for the tail loop.
// Poll errors are swallowed (return empty logsTailMsg) so transient issues do
// not tear down the follow session.
func pollLogs(c *awsx.Clients, cfg awsx.AwsLogs, container string, lastTs int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		prefix := ""
		if cfg.Prefix != "" {
			prefix = cfg.Prefix + "/" + container
		}
		events, err := awsx.FilterLogEvents(ctx, c.Logs, cfg.Group, prefix, lastTs+1, 300)
		if err != nil {
			return logsTailMsg{lastTs: lastTs}
		}
		sort.Slice(events, func(i, j int) bool { return events[i].Timestamp < events[j].Timestamp })
		newLast := lastTs
		lines := make([]string, 0, len(events))
		for _, e := range events {
			lines = append(lines, fmtLogEvent(e))
			if e.Timestamp > newLast {
				newLast = e.Timestamp
			}
		}
		return logsTailMsg{lines: lines, lastTs: newLast}
	}
}

func tickLogs(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return logsTickMsg{} })
}

func fmtLogEvent(e awsx.LogEvent) string {
	t := time.UnixMilli(e.Timestamp).Local()
	return t.Format("01-02 15:04:05") + "  " + strings.TrimRight(e.Message, " \t\r\n")
}

// Log level styling (mirrors python/awscx/render.py:log_level_style).

var (
	levelJSONRE = regexp.MustCompile(`(?i)"level"\s*:\s*"?([A-Za-z]+)`)
	levelWordRE = regexp.MustCompile(`(?i)\b(ERROR|WARNING|WARN|INFO|DEBUG|TRACE|FATAL|CRITICAL|SEVERE|NOTICE)\b`)

	logErrStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	logWarnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	logInfoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	logDebugStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	logMatchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(lipgloss.Color("226"))
)

func logLevelStyle(line string) lipgloss.Style {
	m := levelJSONRE.FindStringSubmatch(line)
	if m == nil {
		m = levelWordRE.FindStringSubmatch(line)
	}
	if m == nil {
		return lipgloss.NewStyle()
	}
	switch strings.ToUpper(m[1]) {
	case "ERROR", "FATAL", "CRITICAL", "SEVERE":
		return logErrStyle
	case "WARN", "WARNING":
		return logWarnStyle
	case "INFO", "NOTICE":
		return logInfoStyle
	case "DEBUG", "TRACE":
		return logDebugStyle
	}
	return lipgloss.NewStyle()
}

// wrapLine hard-wraps a raw (no-ANSI) line to the given rune-count width.
// bubbles/viewport doesn't soft-wrap, so long log lines get truncated at the
// right edge unless we pre-wrap them.
func wrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	runes := []rune(line)
	if len(runes) <= width {
		return []string{line}
	}
	out := make([]string, 0, len(runes)/width+1)
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

// highlightSearch renders line with search matches highlighted while keeping
// the surrounding text in base style. Case-insensitive, ASCII byte offsets
// (fine for typical log searches).
func highlightSearch(line, search string, base lipgloss.Style) string {
	if search == "" {
		return base.Render(line)
	}
	low := strings.ToLower(line)
	needle := strings.ToLower(search)
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(low[i:], needle)
		if j < 0 {
			b.WriteString(base.Render(line[i:]))
			return b.String()
		}
		j += i
		if j > i {
			b.WriteString(base.Render(line[i:j]))
		}
		b.WriteString(logMatchStyle.Render(line[j : j+len(needle)]))
		i = j + len(needle)
	}
}