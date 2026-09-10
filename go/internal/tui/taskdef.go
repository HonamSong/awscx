package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

type taskdefLoadedMsg struct {
	arn string
	td  *ecstypes.TaskDefinition
}

// fetchTaskdef mirrors Python show_taskdef: prefer the service's active
// taskDefinition, fall back to the first running task's taskDefinitionArn.
func fetchTaskdef(c *awsx.Clients, cluster, service string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()

		var tdARN string
		if service != "" {
			svc, err := awsx.DescribeService(ctx, c.ECS, cluster, service)
			if err != nil {
				return errMsg{fmt.Errorf("describe service: %w", err)}
			}
			if svc != nil {
				tdARN = awssdk.ToString(svc.TaskDefinition)
			}
		}
		if tdARN == "" {
			tasks, err := awsx.ListRunningTasks(ctx, c.ECS, cluster, service)
			if err != nil {
				return errMsg{fmt.Errorf("list tasks: %w", err)}
			}
			if len(tasks) > 0 {
				tdARN = awssdk.ToString(tasks[0].TaskDefinitionArn)
			}
		}
		if tdARN == "" {
			return errMsg{fmt.Errorf("task definition 을 찾을 수 없습니다")}
		}
		td, err := awsx.GetTaskDefinition(ctx, c.ECS, tdARN)
		if err != nil {
			return errMsg{fmt.Errorf("task def: %w", err)}
		}
		return taskdefLoadedMsg{arn: tdARN, td: td}
	}
}

// renderTaskdef pretty-prints the task definition. SDK v2 types serialize with
// PascalCase field names; we round-trip through a generic tree and lowercase
// the first char of each key to match the boto3 (camelCase) output that
// Python users are used to.
func renderTaskdef(td *ecstypes.TaskDefinition) string {
	raw, err := json.Marshal(td)
	if err != nil {
		return "(직렬화 실패: " + err.Error() + ")"
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return string(raw)
	}
	lowerFirstKeys(tree)
	pretty, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return string(raw)
	}
	return colorizeJSON(string(pretty))
}

// colorizeJSON applies ANSI colors to a JSON string. Hand-rolled tokenizer
// (no chroma dep). Handles: keys (cyan bold), strings (green), numbers
// (yellow), true/false/null (magenta), braces/commas/whitespace (default).
func colorizeJSON(src string) string {
	var b strings.Builder
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(src) {
				if src[j] == '\\' && j+1 < len(src) {
					j += 2
					continue
				}
				if src[j] == '"' {
					j++
					break
				}
				j++
			}
			tok := src[i:j]
			k := j
			for k < len(src) && (src[k] == ' ' || src[k] == '\t') {
				k++
			}
			if k < len(src) && src[k] == ':' {
				b.WriteString(jsonKeyStyle.Render(tok))
			} else {
				b.WriteString(jsonStrStyle.Render(tok))
			}
			i = j

		case (c >= '0' && c <= '9') || (c == '-' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9'):
			j := i
			if c == '-' {
				j++
			}
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			if j < len(src) && src[j] == '.' {
				j++
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
			}
			if j < len(src) && (src[j] == 'e' || src[j] == 'E') {
				j++
				if j < len(src) && (src[j] == '+' || src[j] == '-') {
					j++
				}
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
			}
			b.WriteString(jsonNumStyle.Render(src[i:j]))
			i = j

		case c == 't' && strings.HasPrefix(src[i:], "true"):
			b.WriteString(jsonKwStyle.Render("true"))
			i += 4
		case c == 'f' && strings.HasPrefix(src[i:], "false"):
			b.WriteString(jsonKwStyle.Render("false"))
			i += 5
		case c == 'n' && strings.HasPrefix(src[i:], "null"):
			b.WriteString(jsonKwStyle.Render("null"))
			i += 4

		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

var (
	jsonKeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	jsonStrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	jsonNumStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	jsonKwStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
)

func lowerFirstKeys(v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		for _, k := range keys {
			val := x[k]
			lowerFirstKeys(val)
			if k == "" {
				continue
			}
			nk := strings.ToLower(k[:1]) + k[1:]
			if nk != k {
				x[nk] = val
				delete(x, k)
			}
		}
	case []any:
		for _, item := range x {
			lowerFirstKeys(item)
		}
	}
}