package tui

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	tea "github.com/charmbracelet/bubbletea"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

// Shell messages.

type shellTargetMsg struct {
	profile   string
	region    string
	cluster   string
	task      string // short ARN (task id)
	container string
	command   string // /bin/sh, etc.
}

type shellExitedMsg struct {
	duration time.Duration
	err      error
}

// fetchShellTarget resolves the first RUNNING task + its first container for
// the given service so exec_into can be dispatched. Mirrors Python's
// resolve_tasks_then_pick auto-select path (single task/container).
func fetchShellTarget(c *awsx.Clients, profile, region, cluster, service, command string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()
		tasks, err := awsx.ListRunningTasks(ctx, c.ECS, cluster, service)
		if err != nil {
			return errMsg{fmt.Errorf("list tasks: %w", err)}
		}
		if len(tasks) == 0 {
			return errMsg{fmt.Errorf("RUNNING task 가 없습니다")}
		}
		t := tasks[0]
		var cname string
		if len(t.Containers) > 0 {
			cname = awssdk.ToString(t.Containers[0].Name)
		}
		if cname == "" {
			return errMsg{fmt.Errorf("container 를 찾을 수 없습니다")}
		}
		return shellTargetMsg{
			profile:   profile,
			region:    region,
			cluster:   cluster,
			task:      awsx.ShortARN(awssdk.ToString(t.TaskArn)),
			container: cname,
			command:   command,
		}
	}
}

// execShell runs `aws ecs execute-command --interactive` with the real TTY
// attached. tea.ExecProcess suspends the bubbletea UI (exits alt-screen)
// while the child runs, then restores it via the callback.
func execShell(t shellTargetMsg) tea.Cmd {
	args := []string{}
	if t.profile != "" {
		args = append(args, "--profile", t.profile)
	}
	args = append(args,
		"ecs", "execute-command",
		"--cluster", t.cluster,
		"--task", t.task,
		"--container", t.container,
		"--interactive",
		"--command", t.command,
	)
	if t.region != "" {
		args = append(args, "--region", t.region)
	}
	start := time.Now()
	cmd := exec.Command("aws", args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return shellExitedMsg{duration: time.Since(start), err: err}
	})
}

// execSSM runs `aws ssm start-session --target <instance-id>` in the same
// suspended-UI fashion as execShell. Prints a two-line context banner
// (instance name/id + default user) before aws CLI outputs its own
// "Starting session with SessionId..." line.
func execSSM(profile, region, instanceID, name, user string) tea.Cmd {
	awsArgs := []string{"aws"}
	if profile != "" {
		awsArgs = append(awsArgs, "--profile", profile)
	}
	awsArgs = append(awsArgs, "ssm", "start-session", "--target", instanceID)
	if region != "" {
		awsArgs = append(awsArgs, "--region", region)
	}

	display := instanceID
	if name != "" {
		display = fmt.Sprintf("%s (%s)", name, instanceID)
	}
	if user == "" {
		user = "ssm-user"
	}
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	// dim 타임스탬프 + green [INFO ] + | + 메시지.
	line1 := fmt.Sprintf("\x1b[2m[%s]\x1b[0m \x1b[32m[INFO ]\x1b[0m | Connecting to: %s", ts, display)
	line2 := fmt.Sprintf("\x1b[2m[%s]\x1b[0m \x1b[32m[INFO ]\x1b[0m | Default user: %s", ts, user)

	// sh -c 'printf ...; shift 2; exec "$@"' _ line1 line2 aws ...args
	//   $0 = _  (placeholder), $1 = line1, $2 = line2, $3.. = aws + args.
	// shift 2 로 line1/line2 를 제거하고 exec "$@" 로 aws 를 대체 실행.
	script := `printf '%s\n' "$1" "$2"; shift 2; exec "$@"`
	shArgs := append([]string{"-c", script, "_", line1, line2}, awsArgs...)

	start := time.Now()
	cmd := exec.Command("sh", shArgs...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return shellExitedMsg{duration: time.Since(start), err: err}
	})
}