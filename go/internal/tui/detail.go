package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

// LBDetail is the per-loadBalancer projection shown in the status pane.
type LBDetail struct {
	ContainerName  string
	ContainerPort  int32
	TargetGroupARN string
	Classic        bool
	ClassicName    string
	Health         *awsx.TGHealth
	Err            error
}

// ServiceDetail aggregates everything the status pane needs. Per-section
// errors are captured so a single failure does not blank the whole view
// (mirrors Python show_status which try/excepts per section).
type ServiceDetail struct {
	ClusterName    string
	ClusterStatus  string
	ServiceName    string
	Service        *ecstypes.Service
	Tasks          []ecstypes.Task
	AutoScaling    *awsx.AutoScaling
	AutoScalingErr error
	CPU            *awsx.MetricSummary
	Memory         *awsx.MetricSummary
	MetricsErr     error
	TaskDef        *ecstypes.TaskDefinition
	TaskDefErr     error
	LB             []LBDetail
}

type detailLoadedMsg struct{ detail ServiceDetail }

// fetchServiceDetail runs all the AWS calls sequentially in a single goroutine.
// Fatal errors on describe_service / list_tasks abort with errMsg; the rest are
// stored per-section.
func fetchServiceDetail(c *awsx.Clients, clusterName, clusterStatus, serviceName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		sd := ServiceDetail{
			ClusterName:   clusterName,
			ClusterStatus: clusterStatus,
			ServiceName:   serviceName,
		}

		svc, err := awsx.DescribeService(ctx, c.ECS, clusterName, serviceName)
		if err != nil {
			return errMsg{fmt.Errorf("describe service: %w", err)}
		}
		sd.Service = svc

		tasks, err := awsx.ListRunningTasks(ctx, c.ECS, clusterName, serviceName)
		if err != nil {
			return errMsg{fmt.Errorf("list tasks: %w", err)}
		}
		sd.Tasks = tasks

		if svc != nil {
			if as, err := awsx.ServiceAutoScaling(ctx, c.AAS, clusterName, serviceName); err != nil {
				sd.AutoScalingErr = err
			} else {
				sd.AutoScaling = as
			}
			if cpu, err := awsx.ServiceMetric(ctx, c.CW, "CPUUtilization", clusterName, serviceName); err != nil {
				sd.MetricsErr = err
			} else {
				sd.CPU = cpu
			}
			if mem, err := awsx.ServiceMetric(ctx, c.CW, "MemoryUtilization", clusterName, serviceName); err != nil {
				if sd.MetricsErr == nil {
					sd.MetricsErr = err
				}
			} else {
				sd.Memory = mem
			}
			if tdARN := awssdk.ToString(svc.TaskDefinition); tdARN != "" {
				if td, err := awsx.GetTaskDefinition(ctx, c.ECS, tdARN); err != nil {
					sd.TaskDefErr = err
				} else {
					sd.TaskDef = td
				}
			}
			for _, lb := range svc.LoadBalancers {
				d := LBDetail{ContainerName: awssdk.ToString(lb.ContainerName)}
				if lb.ContainerPort != nil {
					d.ContainerPort = *lb.ContainerPort
				}
				tg := awssdk.ToString(lb.TargetGroupArn)
				if tg == "" {
					d.Classic = true
					d.ClassicName = awssdk.ToString(lb.LoadBalancerName)
				} else {
					d.TargetGroupARN = tg
					if info, err := awsx.TargetGroupHealth(ctx, c.ELB, tg); err != nil {
						d.Err = err
					} else {
						d.Health = info
					}
				}
				sd.LB = append(sd.LB, d)
			}
		}
		return detailLoadedMsg{detail: sd}
	}
}

var (
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	boldStyle = lipgloss.NewStyle().Bold(true)
	sectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	cyanStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51"))
	redStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

func statusStyle(s string) lipgloss.Style {
	switch strings.ToUpper(s) {
	case "ACTIVE", "RUNNING", "PRIMARY", "COMPLETED", "HEALTHY":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	case "DRAINING", "PENDING", "IN_PROGRESS", "PROVISIONING", "UNKNOWN":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	}
}

func pctStyle(v float64) lipgloss.Style {
	if v >= 85 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	}
	if v >= 60 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
}

// renderServiceDetail formats ServiceDetail into an ANSI-styled string.
func renderServiceDetail(sd ServiceDetail) string {
	var b strings.Builder

	b.WriteString(dimStyle.Render("Cluster : "))
	b.WriteString(sd.ClusterName + "  (")
	b.WriteString(statusStyle(sd.ClusterStatus).Render(sd.ClusterStatus))
	b.WriteString(")\n")

	svc := sd.Service
	if svc == nil {
		b.WriteString(statusStyle("PENDING").Render("(service 없음 - standalone task)") + "\n")
	} else {
		writeServiceBlock(&b, sd, svc)
	}

	b.WriteString("\n" + sectStyle.Render(fmt.Sprintf("Running tasks (%d):", len(sd.Tasks))) + "\n")
	for _, t := range sd.Tasks {
		b.WriteString(fmt.Sprintf("  - %s  ", awsx.ShortARN(awssdk.ToString(t.TaskArn))))
		ls := awssdk.ToString(t.LastStatus)
		b.WriteString(statusStyle(ls).Render(ls))
		b.WriteString(dimStyle.Render("  health="))
		health := string(t.HealthStatus)
		if health == "" {
			health = "-"
		}
		b.WriteString(statusStyle(health).Render(health))
		b.WriteString(dimStyle.Render("  exec="))
		if t.EnableExecuteCommand {
			b.WriteString(statusStyle("HEALTHY").Render("ON"))
		} else {
			b.WriteString(redStyle.Render("OFF"))
		}
		var cnames []string
		for _, cc := range t.Containers {
			cnames = append(cnames, awssdk.ToString(cc.Name))
		}
		b.WriteString(dimStyle.Render(fmt.Sprintf("  containers=[%s]\n", strings.Join(cnames, ","))))
	}
	return b.String()
}

func writeServiceBlock(b *strings.Builder, sd ServiceDetail, svc *ecstypes.Service) {
	// Header
	b.WriteString(dimStyle.Render("Service : "))
	b.WriteString(awssdk.ToString(svc.ServiceName) + "  (")
	status := awssdk.ToString(svc.Status)
	b.WriteString(statusStyle(status).Render(status))
	b.WriteString(")\n")

	running, desired, pending := svc.RunningCount, svc.DesiredCount, svc.PendingCount
	b.WriteString(dimStyle.Render("Count   : "))
	countStyle := "PENDING"
	if running == desired {
		countStyle = "HEALTHY"
	}
	b.WriteString(statusStyle(countStyle).Render(fmt.Sprintf("%d/%d", running, desired)))
	b.WriteString(dimStyle.Render(fmt.Sprintf("  (desired=%d running=%d pending=%d)\n", desired, running, pending)))

	lt := "-"
	if svc.LaunchType != "" {
		lt = string(svc.LaunchType)
	}
	b.WriteString(dimStyle.Render("LaunchType : ") + lt + "\n")
	b.WriteString(dimStyle.Render("TaskDef : ") + awsx.ShortARN(awssdk.ToString(svc.TaskDefinition)) + "\n")

	// Deployments
	b.WriteString("\n" + sectStyle.Render("Deployments:") + "\n")
	for _, d := range svc.Deployments {
		b.WriteString("  - ")
		st := awssdk.ToString(d.Status)
		b.WriteString(statusStyle(st).Render(st))
		b.WriteString(fmt.Sprintf("  %s  desired=%d running=%d ",
			awsx.ShortARN(awssdk.ToString(d.TaskDefinition)), d.DesiredCount, d.RunningCount))
		rollout := string(d.RolloutState)
		if rollout == "" {
			rollout = "-"
		}
		b.WriteString(statusStyle(rollout).Render("rollout="+rollout) + "\n")
	}

	// Auto scaling
	b.WriteString("\n" + sectStyle.Render("Auto Scaling:") + "\n")
	switch {
	case sd.AutoScalingErr != nil:
		b.WriteString(redStyle.Render(fmt.Sprintf("  (autoscaling 조회 실패: %v)\n", sd.AutoScalingErr)))
	case sd.AutoScaling == nil:
		b.WriteString(dimStyle.Render("  미설정 (고정 desired count)\n"))
	default:
		var min, max int32
		if sd.AutoScaling.Min != nil {
			min = *sd.AutoScaling.Min
		}
		if sd.AutoScaling.Max != nil {
			max = *sd.AutoScaling.Max
		}
		b.WriteString(dimStyle.Render("  용량 : "))
		b.WriteString(cyanStyle.Render(fmt.Sprintf("min=%d  max=%d\n", min, max)))
		if len(sd.AutoScaling.Policies) == 0 {
			b.WriteString(dimStyle.Render("  (scalable target 만 있고 정책 없음)\n"))
		}
		for _, p := range sd.AutoScaling.Policies {
			b.WriteString(fmt.Sprintf("  - %s ", awssdk.ToString(p.PolicyName)))
			if string(p.PolicyType) == "TargetTrackingScaling" && p.TargetTrackingScalingPolicyConfiguration != nil {
				cfg := p.TargetTrackingScalingPolicyConfiguration
				metric := "custom"
				if cfg.PredefinedMetricSpecification != nil {
					metric = string(cfg.PredefinedMetricSpecification.PredefinedMetricType)
				}
				var target float64
				if cfg.TargetValue != nil {
					target = *cfg.TargetValue
				}
				b.WriteString(statusStyle("HEALTHY").Render(fmt.Sprintf("target=%.1f %s\n", target, metric)))
			} else {
				b.WriteString(statusStyle("PENDING").Render(fmt.Sprintf("(%s)\n", p.PolicyType)))
			}
		}
	}

	// Monitoring
	b.WriteString("\n" + sectStyle.Render("Monitoring (최근 1시간, CloudWatch):") + "\n")
	if sd.MetricsErr != nil {
		b.WriteString(redStyle.Render(fmt.Sprintf("  (지표 조회 실패: %v)\n", sd.MetricsErr)))
	} else {
		for _, m := range []struct {
			label string
			s     *awsx.MetricSummary
		}{{"CPU   ", sd.CPU}, {"Memory", sd.Memory}} {
			b.WriteString(dimStyle.Render("  " + m.label + " : "))
			if m.s == nil {
				b.WriteString(dimStyle.Render("(데이터 없음)\n"))
				continue
			}
			b.WriteString(pctStyle(m.s.Avg).Render(fmt.Sprintf("avg=%.1f%%", m.s.Avg)))
			b.WriteString("  ")
			b.WriteString(pctStyle(m.s.Peak).Render(fmt.Sprintf("peak=%.1f%%\n", m.s.Peak)))
		}
	}

	// TaskDef
	b.WriteString("\n" + sectStyle.Render("TaskDef 설정:") + "\n")
	if sd.TaskDefErr != nil {
		b.WriteString(redStyle.Render(fmt.Sprintf("  (taskdef 조회 실패: %v)\n", sd.TaskDefErr)))
	} else if sd.TaskDef != nil {
		td := sd.TaskDef
		cpu := awssdk.ToString(td.Cpu)
		if cpu == "" {
			cpu = "-"
		}
		mem := awssdk.ToString(td.Memory)
		if mem == "" {
			mem = "-"
		}
		nm := string(td.NetworkMode)
		if nm == "" {
			nm = "-"
		}
		b.WriteString(dimStyle.Render("  Task 크기 : "))
		b.WriteString(fmt.Sprintf("cpu=%s units  memory=%s MB  network=%s\n", cpu, mem, nm))
		for _, cd := range td.ContainerDefinitions {
			var pps []string
			for _, pm := range cd.PortMappings {
				var s string
				if pm.ContainerPort != nil {
					s = fmt.Sprintf("%d", *pm.ContainerPort)
				}
				if pm.HostPort != nil {
					s += fmt.Sprintf("->%d", *pm.HostPort)
				}
				if pm.Protocol != "" {
					s += "/" + string(pm.Protocol)
				}
				if s != "" {
					pps = append(pps, s)
				}
			}
			ports := "-"
			if len(pps) > 0 {
				ports = strings.Join(pps, ", ")
			}
			ccpu := "-"
			if cd.Cpu != 0 {
				ccpu = fmt.Sprintf("%d", cd.Cpu)
			}
			cmem := "-"
			if cd.Memory != nil {
				cmem = fmt.Sprintf("%d", *cd.Memory)
			} else if cd.MemoryReservation != nil {
				cmem = fmt.Sprintf("%d", *cd.MemoryReservation)
			}
			b.WriteString("  - ")
			b.WriteString(boldStyle.Render(awssdk.ToString(cd.Name)))
			b.WriteString(dimStyle.Render(fmt.Sprintf(": cpu=%s  mem=%s  ", ccpu, cmem)))
			b.WriteString("ports=" + cyanStyle.Render(ports) + "\n")
		}
	}

	// Load Balancer
	b.WriteString("\n" + sectStyle.Render("Load Balancer:") + "\n")
	if len(svc.LoadBalancers) == 0 {
		b.WriteString(dimStyle.Render("  연결 없음\n"))
	} else {
		for _, lb := range sd.LB {
			if lb.Classic {
				b.WriteString(fmt.Sprintf("  - classic LB %s  (%s:%d)\n",
					lb.ClassicName, lb.ContainerName, lb.ContainerPort))
				continue
			}
			b.WriteString(fmt.Sprintf("  - %s:%d  →  TG %s\n",
				lb.ContainerName, lb.ContainerPort, awsx.ShortARN(lb.TargetGroupARN)))
			if lb.Err != nil {
				b.WriteString(redStyle.Render(fmt.Sprintf("      (LB 조회 실패: %v)\n", lb.Err)))
				continue
			}
			if lb.Health == nil {
				continue
			}
			b.WriteString(dimStyle.Render("      targets: "))
			if len(lb.Health.Counts) == 0 {
				b.WriteString(statusStyle("PENDING").Render("대상 없음") + "\n")
			} else {
				parts := make([]string, 0, len(lb.Health.Counts))
				for k, v := range lb.Health.Counts {
					parts = append(parts, statusStyle(k).Render(fmt.Sprintf("%s=%d", k, v)))
				}
				b.WriteString(strings.Join(parts, ", ") + "\n")
			}
			if lb.Health.LB != nil {
				info := lb.Health.LB
				b.WriteString(dimStyle.Render("      LB "))
				b.WriteString(fmt.Sprintf("%s (", info.Name))
				b.WriteString(statusStyle(info.State).Render(info.State))
				b.WriteString(dimStyle.Render(fmt.Sprintf(")  %s\n", info.DNS)))
			}
		}
	}

	// Events (max 5)
	events := svc.Events
	if len(events) > 5 {
		events = events[:5]
	}
	if len(events) > 0 {
		b.WriteString("\n" + sectStyle.Render("Recent events:") + "\n")
		for _, e := range events {
			b.WriteString("  - ")
			if e.CreatedAt != nil {
				b.WriteString(dimStyle.Render(e.CreatedAt.Format("2006-01-02 15:04:05") + "  "))
			}
			b.WriteString(awssdk.ToString(e.Message) + "\n")
		}
	}
}