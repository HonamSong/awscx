package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
)

// sparkRunes matches Python awscx.render._SPARK_BLOCKS.
var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// MonitorData mirrors Python _monitor_worker's collected series.
type MonitorData struct {
	CPU    []float64
	Memory []float64
	NetRx  []float64
	NetTx  []float64
	DiskR  []float64
	DiskW  []float64
	Err    error
	Stamp  time.Time
}

type monitorLoadedMsg struct{ data MonitorData }
type monitorTickMsg struct{}

// fetchMonitor collects CPU/Memory (AWS/ECS) and Network/Disk
// (ECS/ContainerInsights) series in one shot. First error aborts (matches
// Python's single try/except).
func fetchMonitor(c *awsx.Clients, cluster, service string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), awsTimeout)
		defer cancel()

		d := MonitorData{Stamp: time.Now()}
		var err error

		if d.CPU, err = awsx.ServiceMetricSeries(ctx, c.CW, "CPUUtilization", cluster, service, 60, 1); err != nil {
			return monitorLoadedMsg{data: MonitorData{Err: err, Stamp: d.Stamp}}
		}
		if d.Memory, err = awsx.ServiceMetricSeries(ctx, c.CW, "MemoryUtilization", cluster, service, 60, 1); err != nil {
			return monitorLoadedMsg{data: MonitorData{Err: err, Stamp: d.Stamp}}
		}
		if d.NetRx, err = awsx.InsightsMetricSeries(ctx, c.CW, "NetworkRxBytes", cluster, service, "", 60, 1); err != nil {
			return monitorLoadedMsg{data: MonitorData{Err: err, Stamp: d.Stamp}}
		}
		if d.NetTx, err = awsx.InsightsMetricSeries(ctx, c.CW, "NetworkTxBytes", cluster, service, "", 60, 1); err != nil {
			return monitorLoadedMsg{data: MonitorData{Err: err, Stamp: d.Stamp}}
		}
		if d.DiskR, err = awsx.InsightsMetricSeries(ctx, c.CW, "StorageReadBytes", cluster, service, "", 60, 1); err != nil {
			return monitorLoadedMsg{data: MonitorData{Err: err, Stamp: d.Stamp}}
		}
		if d.DiskW, err = awsx.InsightsMetricSeries(ctx, c.CW, "StorageWriteBytes", cluster, service, "", 60, 1); err != nil {
			return monitorLoadedMsg{data: MonitorData{Err: err, Stamp: d.Stamp}}
		}
		return monitorLoadedMsg{data: d}
	}
}

func tickMonitor(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return monitorTickMsg{} })
}

var (
	labelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	greenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	magentaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("213"))
)

// maxSpark caps the sparkline width so a narrow pane doesn't wrap.
const maxSpark = 40

func renderMonitor(d MonitorData) string {
	var b strings.Builder
	if d.Err != nil {
		b.WriteString(redStyle.Render(fmt.Sprintf("(조회 실패: %v)", d.Err)))
		return b.String()
	}
	pctRow(&b, "CPU   ", d.CPU)
	pctRow(&b, "Memory", d.Memory)
	rateRow(&b, "Net Rx", d.NetRx)
	rateRow(&b, "Net Tx", d.NetTx)
	rateRow(&b, "Dsk R ", d.DiskR)
	rateRow(&b, "Dsk W ", d.DiskW)
	b.WriteString(dimStyle.Render(fmt.Sprintf("최근 1시간·1분 · 갱신 %s",
		d.Stamp.Local().Format("15:04:05"))))
	return b.String()
}

// pctRow emits a 2-line block: header (label + now/avg/peak) then a sparkline.
// Keeping each line short so the pane doesn't wrap.
func pctRow(b *strings.Builder, label string, vals []float64) {
	b.WriteString(labelStyle.Render(label))
	if len(vals) == 0 {
		b.WriteString(" " + dimStyle.Render("(데이터 없음)") + "\n")
		return
	}
	now := vals[len(vals)-1]
	var sum, peak float64
	for _, v := range vals {
		sum += v
		if v > peak {
			peak = v
		}
	}
	avg := sum / float64(len(vals))

	b.WriteString("  now=")
	b.WriteString(pctStyle(now).Render(fmt.Sprintf("%5.1f%%", now)))
	b.WriteString(fmt.Sprintf(" avg=%5.1f%% peak=", avg))
	b.WriteString(pctStyle(peak).Render(fmt.Sprintf("%5.1f%%", peak)))
	b.WriteByte('\n')

	// sparkline on its own line, truncated from the tail if too long.
	hi := peak
	if hi == 0 {
		hi = 1
	}
	last := len(sparkRunes) - 1
	tail := vals
	if len(tail) > maxSpark {
		tail = tail[len(tail)-maxSpark:]
	}
	b.WriteString("        ")
	for _, v := range tail {
		idx := int(v / hi * float64(last))
		if idx < 0 {
			idx = 0
		} else if idx > last {
			idx = last
		}
		b.WriteString(pctStyle(v).Render(string(sparkRunes[idx])))
	}
	b.WriteByte('\n')
}

// rateRow emits a single narrow line for one network/disk direction.
func rateRow(b *strings.Builder, label string, vals []float64) {
	b.WriteString(labelStyle.Render(label))
	if len(vals) == 0 {
		b.WriteString(" " + dimStyle.Render("-") + "\n")
		return
	}
	cur := humanizeBytes(vals[len(vals)-1]/60) + "/s"
	var peak float64
	for _, x := range vals {
		if x > peak {
			peak = x
		}
	}
	pk := humanizeBytes(peak/60) + "/s"
	b.WriteString("  ")
	b.WriteString(greenStyle.Render("now=" + cur))
	b.WriteString("  ")
	b.WriteString(magentaStyle.Render("peak="+pk) + "\n")
}

func humanizeBytes(n float64) string {
	for _, unit := range []string{"B", "KB", "MB", "GB", "TB"} {
		if n < 1024 {
			return fmt.Sprintf("%.1f%s", n, unit)
		}
		n /= 1024
	}
	return fmt.Sprintf("%.1fPB", n)
}