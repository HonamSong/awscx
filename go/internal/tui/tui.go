// Package tui hosts the bubbletea-based UI (MVP: list navigation only).
// Screens: profile → mode → clusters → services (ECS) / ec2 (EC2).
// Later phases will add service status, monitor, logs, embedded shell.
package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	awsx "github.com/HonamSong/awscx/go/internal/aws"
	"github.com/HonamSong/awscx/go/internal/config"
)

type screen int

const (
	screenProfile screen = iota
	screenMode
	screenClusters
	screenServices
	screenEC2
	screenServiceDetail
	screenLogs
	screenTaskdef
	screenMonitor
	screenConfig
	screenVPC
	screenVPCDetail
	screenSecrets
	screenSecretDetail
	screenR53Zones
	screenR53Records
)

var (
	styleTop    = lipgloss.NewStyle().Background(lipgloss.Color("62")).Foreground(lipgloss.Color("230")).Padding(0, 1)
	styleStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 1)
	styleErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleHelp   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleMonPane = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)
	styleMonTitle = lipgloss.NewStyle().Foreground(lipgloss.Color("62")).Bold(true)
)

// monitorPaneW returns the right-pane width tuned to the current terminal.
// 넓은 화면(≥120)에서는 52col, 좁으면 40col.
func (m Model) monitorPaneW() int {
	if m.width >= 120 {
		return 52
	}
	return 40
}

// monitorVisible reports whether the service list should render with a
// right-side monitor pane. Python always splits — we only skip when the
// terminal is truly too narrow to fit both.
func (m Model) monitorVisible() bool {
	return m.screen == screenServices && len(m.services) > 0 &&
		m.width >= m.monitorPaneW()+30
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Model is the bubbletea model for the whole app.
type Model struct {
	version string
	region  string
	command string // shell command to exec into (default /bin/sh)

	profile       string
	cluster       string
	clusterStatus string
	service       string

	screen screen
	stack  []screen

	clients *awsx.Clients

	profiles []awsx.ProfileToken
	clusters []awsx.ClusterRow
	services []awsx.ServiceRow
	ec2      []awsx.EC2Instance

	// profiles list custom renderer
	profilesFiltered []awsx.ProfileToken
	profilesCursor   int
	profilesView     viewport.Model

	// services list custom renderer (bypasses bubbles/table for color support)
	servicesFiltered []awsx.ServiceRow
	servicesCursor   int
	servicesView     viewport.Model

	// ec2 list custom renderer (same ANSI-truncate workaround)
	ec2Filtered []awsx.EC2Instance
	ec2Cursor   int
	ec2View     viewport.Model

	// VPC (read-only)
	vpcs         []awsx.VPCRow
	vpcsFiltered []awsx.VPCRow
	vpcsCursor   int
	vpcsView     viewport.Model

	// Secrets Manager (read-only, metadata only — 값은 조회 안 함)
	secrets         []awsx.SecretRow
	secretsFiltered []awsx.SecretRow
	secretsCursor   int
	secretsView     viewport.Model

	// Route53 hosted zones (read-only)
	r53Zones         []awsx.R53ZoneRow
	r53ZonesFiltered []awsx.R53ZoneRow
	r53ZonesCursor   int
	r53ZonesView     viewport.Model

	// VPC detail (subnets + NAT gateways) — scrollable text
	vpcDetailID   string
	vpcDetailName string
	vpcDetailRaw  string
	vpcDetailView viewport.Model

	// Secret detail — scrollable text + optional value reveal
	secretDetailName    string
	secretDetail        *awsx.SecretDetail
	secretDetailRaw     string
	secretDetailView    viewport.Model
	secretValueRevealed bool
	secretValue         string
	secretValueErr      error

	// Route53 records (drill-down from zones)
	r53CurrentZoneID   string
	r53CurrentZoneName string
	r53Records         []awsx.R53RecordRow
	r53RecordsFiltered []awsx.R53RecordRow
	r53RecordsCursor   int
	r53RecordsView     viewport.Model

	// service list ↔ monitor pane sync
	currentMonitorSvc string

	table       table.Model
	filter      textinput.Model
	spinner     spinner.Model
	detailView  viewport.Model
	detailRaw   string // colorized detail text, unwrapped
	logView     viewport.Model
	taskdefView viewport.Model
	taskdefARN  string
	taskdefRaw  string // colorized JSON, unwrapped (source for width-aware re-wrap)
	filtering   bool
	loading    bool
	status     string
	err        error

	// log viewer state
	logCfg       awsx.AwsLogs
	logContainer string
	logLines     []string
	logLastTs    int64
	logFollow    bool
	logSearch    string
	tailInterval time.Duration

	// monitor state
	monitorContent  string
	monitorInterval time.Duration

	// config screen state
	cfg         config.Config
	editInput   textinput.Model
	editingIdx  int // -1 = not editing; else index into settings

	// digit-jump buffer (프로파일/클러스터/서비스/EC2 목록에서 번호 키로 즉시 이동)
	numBuf string


	width, height int
}

// New builds the initial model. profile may be empty to force the picker.
// cfg supplies region/command/intervals/log settings and is the persistence
// target for the config editor screen.
func New(version, profile string, cfg config.Config) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 128
	ei := textinput.New()
	ei.CharLimit = 256
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	tail := time.Duration(cfg.TailIntervalSec) * time.Second
	if tail < time.Second {
		tail = 3 * time.Second
	}
	mon := time.Duration(cfg.MonitorIntervalSec) * time.Second
	if mon < 5*time.Second {
		mon = 30 * time.Second
	}
	shellCmd := cfg.Command
	if shellCmd == "" {
		shellCmd = "/bin/sh"
	}

	m := Model{
		version:         version,
		region:          cfg.Region,
		command:         shellCmd,
		profile:         profile,
		cfg:             cfg,
		filter:          ti,
		editInput:       ei,
		editingIdx:      -1,
		spinner:         sp,
		detailView:      viewport.New(80, 20),
		logView:         viewport.New(80, 20),
		taskdefView:     viewport.New(80, 20),
		servicesView:    viewport.New(80, 20),
		ec2View:         viewport.New(80, 20),
		profilesView:    viewport.New(80, 20),
		vpcsView:         viewport.New(80, 20),
		secretsView:      viewport.New(80, 20),
		r53ZonesView:     viewport.New(80, 20),
		vpcDetailView:    viewport.New(80, 20),
		secretDetailView: viewport.New(80, 20),
		r53RecordsView:   viewport.New(80, 20),
		tailInterval:    tail,
		monitorInterval: mon,
	}
	if profile == "" {
		m.screen = screenProfile
	} else {
		m.screen = screenMode
	}
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	switch m.screen {
	case screenProfile:
		m.loading = true
		cmds = append(cmds, fetchProfiles())
	case screenMode:
		m.loading = true
		cmds = append(cmds, loadClients(m.profile, m.region))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.detailView.Width = msg.Width
		m.logView.Width = msg.Width
		m.taskdefView.Width = msg.Width
		m.servicesView.Width = msg.Width
		m.ec2View.Width = msg.Width
		m.profilesView.Width = msg.Width
		m.vpcsView.Width = msg.Width
		m.secretsView.Width = msg.Width
		m.r53ZonesView.Width = msg.Width
		m.vpcDetailView.Width = msg.Width
		m.secretDetailView.Width = msg.Width
		m.r53RecordsView.Width = msg.Width
		if h := msg.Height - 4; h > 0 {
			m.detailView.Height = h
			m.logView.Height = h
			m.taskdefView.Height = h
			m.servicesView.Height = h
			m.ec2View.Height = h
			m.profilesView.Height = h
			m.vpcsView.Height = h
			m.secretsView.Height = h
			m.r53ZonesView.Height = h
			m.vpcDetailView.Height = h
			m.secretDetailView.Height = h
			m.r53RecordsView.Height = h
		}
		m.rebuildTable() // resize columns
		if m.screen == screenLogs {
			m.rebuildLogView() // rewrap on resize
		}
		if m.screen == screenTaskdef {
			m.rebuildTaskdefView()
		}
		if m.screen == screenServiceDetail {
			m.rebuildDetailView()
		}
		if m.screen == screenServices {
			m.rebuildServicesView()
		}
		if m.screen == screenEC2 {
			m.rebuildEC2View()
		}
		if m.screen == screenProfile {
			m.rebuildProfileView()
		}
		if m.screen == screenVPC {
			m.rebuildVPCView()
		}
		if m.screen == screenSecrets {
			m.rebuildSecretsView()
		}
		if m.screen == screenR53Zones {
			m.rebuildR53View()
		}
		if m.screen == screenR53Records {
			m.rebuildR53RecordsView()
		}
		if m.screen == screenVPCDetail {
			m.rebuildVPCDetailView()
		}
		if m.screen == screenSecretDetail {
			m.rebuildSecretDetailView()
		}
		return m, nil

	case detailLoadedMsg:
		m.detailRaw = renderServiceDetail(msg.detail)
		m.rebuildDetailView()
		m.detailView.GotoTop()
		m.loading = false
		return m, nil

	case logsLoadedMsg:
		m.logCfg = msg.cfg
		m.logContainer = msg.container
		m.logLines = msg.lines
		m.logLastTs = msg.lastTs
		m.logFollow = false
		m.logSearch = ""
		m.filter.SetValue("")
		m.rebuildLogView()
		m.logView.GotoBottom()
		m.loading = false
		return m, nil

	case logsTailMsg:
		if len(msg.lines) > 0 {
			m.logLines = append(m.logLines, msg.lines...)
			if len(m.logLines) > 2000 {
				m.logLines = m.logLines[len(m.logLines)-2000:]
			}
			if msg.lastTs > m.logLastTs {
				m.logLastTs = msg.lastTs
			}
			m.rebuildLogView()
			m.logView.GotoBottom()
		}
		return m, nil

	case logsTickMsg:
		if m.screen != screenLogs || !m.logFollow {
			return m, nil
		}
		return m, tea.Batch(
			pollLogs(m.clients, m.logCfg, m.logContainer, m.logLastTs),
			tickLogs(m.tailInterval),
		)

	case taskdefLoadedMsg:
		m.taskdefARN = msg.arn
		m.taskdefRaw = renderTaskdef(msg.td)
		m.rebuildTaskdefView()
		m.taskdefView.GotoTop()
		m.loading = false
		return m, nil

	case monitorLoadedMsg:
		m.monitorContent = renderMonitor(msg.data)
		m.loading = false
		return m, nil

	case monitorTickMsg:
		switch m.screen {
		case screenMonitor:
			return m, tea.Batch(
				fetchMonitor(m.clients, m.cluster, m.service),
				tickMonitor(m.monitorInterval),
			)
		case screenServices:
			if m.currentMonitorSvc == "" {
				return m, nil
			}
			return m, tea.Batch(
				fetchMonitor(m.clients, m.cluster, m.currentMonitorSvc),
				tickMonitor(m.monitorInterval),
			)
		}
		return m, nil

	case shellTargetMsg:
		m.loading = false
		m.status = fmt.Sprintf("shell 접속중: %s/%s (F10/exit 로 복귀)", msg.cluster, msg.container)
		return m, execShell(msg)

	case shellExitedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("세션 종료 (에러: %v · %.1fs)", msg.err, msg.duration.Seconds())
		} else if msg.duration < 3*time.Second {
			m.status = fmt.Sprintf("세션 조기 종료 (%.1fs · 접속 실패 가능성)", msg.duration.Seconds())
		} else {
			m.status = fmt.Sprintf("세션 종료 (%.1fs)", msg.duration.Seconds())
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case profilesLoadedMsg:
		m.profiles = msg.profiles
		m.profilesCursor = 0
		m.profilesView.YOffset = 0
		m.loading = false
		m.rebuildTable() // → rebuildProfileView for profile screen
		return m, nil

	case clientsLoadedMsg:
		m.clients = msg.clients
		m.loading = false
		return m, nil

	case clustersLoadedMsg:
		m.clusters = nil
		for _, c := range msg.clusters {
			m.clusters = append(m.clusters, awsx.ClusterRow{
				Name:    awssdk.ToString(c.ClusterName),
				Status:  awssdk.ToString(c.Status),
				Running: c.RunningTasksCount,
				Pending: c.PendingTasksCount,
				Active:  c.ActiveServicesCount,
			})
		}
		m.loading = false
		m.rebuildTable()
		return m, nil

	case servicesLoadedMsg:
		m.services = msg.services
		m.servicesCursor = 0
		m.servicesView.YOffset = 0
		m.loading = false
		m.rebuildTable() // → rebuildServicesView for services screen
		// 첫 서비스로 monitor 자동 시작 (우측 패널)
		if len(m.servicesFiltered) > 0 && m.screen == screenServices {
			m.currentMonitorSvc = m.servicesFiltered[0].Name
			m.monitorContent = ""
			return m, tea.Batch(
				fetchMonitor(m.clients, m.cluster, m.currentMonitorSvc),
				tickMonitor(m.monitorInterval),
			)
		}
		m.currentMonitorSvc = ""
		m.monitorContent = ""
		return m, nil

	case ec2LoadedMsg:
		m.ec2 = msg.instances
		m.ec2Cursor = 0
		m.ec2View.YOffset = 0
		m.loading = false
		m.rebuildTable() // → rebuildEC2View for ec2 screen
		return m, nil

	case vpcsLoadedMsg:
		m.vpcs = msg.rows
		m.vpcsCursor = 0
		m.vpcsView.YOffset = 0
		m.loading = false
		m.rebuildVPCView()
		return m, nil

	case secretsLoadedMsg:
		m.secrets = msg.rows
		m.secretsCursor = 0
		m.secretsView.YOffset = 0
		m.loading = false
		m.rebuildSecretsView()
		return m, nil

	case r53ZonesLoadedMsg:
		m.r53Zones = msg.rows
		m.r53ZonesCursor = 0
		m.r53ZonesView.YOffset = 0
		m.loading = false
		m.rebuildR53View()
		return m, nil

	case vpcDetailLoadedMsg:
		m.vpcDetailID = msg.vpcID
		m.vpcDetailName = msg.vpcName
		m.vpcDetailRaw = renderVPCDetail(msg.vpcID, msg.vpcName, msg.cidr, msg.subnets, msg.natgws)
		m.rebuildVPCDetailView()
		m.vpcDetailView.GotoTop()
		m.loading = false
		return m, nil

	case secretDetailLoadedMsg:
		m.secretDetail = msg.detail
		m.secretValueRevealed = false
		m.secretValue = ""
		m.secretValueErr = nil
		m.rebuildSecretDetailView()
		m.secretDetailView.GotoTop()
		m.loading = false
		return m, nil

	case secretValueLoadedMsg:
		if msg.name != m.secretDetailName {
			return m, nil // stale (사용자가 이미 다른 secret 로 이동함)
		}
		m.secretValueRevealed = true
		m.secretValue = msg.value
		m.secretValueErr = msg.err
		m.rebuildSecretDetailView()
		return m, nil

	case r53RecordsLoadedMsg:
		m.r53Records = msg.rows
		m.r53RecordsCursor = 0
		m.r53RecordsView.YOffset = 0
		m.loading = false
		m.rebuildR53RecordsView()
		return m, nil

	case errMsg:
		m.err = msg.err
		m.loading = false
		return m, nil

	case numBufClearMsg:
		if m.numBuf == msg.buf {
			m.numBuf = ""
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleDigit handles a single digit press for the list screens. Appends to
// numBuf, jumps the cursor to that 1-based index (if in range) on the
// current screen, and schedules a timeout to clear the buffer so multi-digit
// (e.g. "12") works if typed within 800ms.
func (m Model) handleDigit(s string) (tea.Model, tea.Cmd) {
	m.numBuf += s
	buf := m.numBuf
	n, _ := strconv.Atoi(buf)
	tick := tea.Tick(800*time.Millisecond, func(time.Time) tea.Msg {
		return numBufClearMsg{buf: buf}
	})
	if n < 1 {
		return m, tick
	}

	switch m.screen {
	case screenProfile:
		if n-1 < len(m.profilesFiltered) {
			m.profilesCursor = n - 1
			m.rebuildProfileView()
		}
	case screenServices:
		if n-1 < len(m.servicesFiltered) {
			m.servicesCursor = n - 1
			tw := m.width
			if m.monitorVisible() {
				tw = m.width - m.monitorPaneW()
			}
			if tw < 30 {
				tw = 30
			}
			m.servicesView.Width = tw
			m.servicesView.SetContent(renderServicesList(m.servicesFiltered, m.servicesCursor, tw))
			m.ensureCursorVisible()
			svc := m.servicesFiltered[m.servicesCursor].Name
			if svc != m.currentMonitorSvc {
				m.currentMonitorSvc = svc
				m.monitorContent = ""
				return m, tea.Batch(fetchMonitor(m.clients, m.cluster, svc), tick)
			}
		}
	case screenEC2:
		if n-1 < len(m.ec2Filtered) {
			m.ec2Cursor = n - 1
			m.rebuildEC2View()
		}
	case screenVPC:
		if n-1 < len(m.vpcsFiltered) {
			m.vpcsCursor = n - 1
			m.rebuildVPCView()
		}
	case screenSecrets:
		if n-1 < len(m.secretsFiltered) {
			m.secretsCursor = n - 1
			m.rebuildSecretsView()
		}
	case screenR53Zones:
		if n-1 < len(m.r53ZonesFiltered) {
			m.r53ZonesCursor = n - 1
			m.rebuildR53View()
		}
	case screenR53Records:
		if n-1 < len(m.r53RecordsFiltered) {
			m.r53RecordsCursor = n - 1
			m.rebuildR53RecordsView()
		}
	case screenClusters, screenMode:
		if n-1 < len(m.table.Rows()) {
			m.table.SetCursor(n - 1)
		}
	}
	return m, tick
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editingIdx >= 0 {
		switch msg.String() {
		case "esc":
			m.editingIdx = -1
			m.editInput.Blur()
			return m, nil
		case "enter":
			idx := m.editingIdx
			m.editingIdx = -1
			m.editInput.Blur()
			s := settings[idx]
			if err := s.setStr(&m.cfg, m.editInput.Value()); err != nil {
				m.status = "잘못된 값: " + err.Error()
				return m, nil
			}
			m.saveAndApply(s.key)
			m.rebuildTable()
			return m, nil
		}
		var cmd tea.Cmd
		m.editInput, cmd = m.editInput.Update(msg)
		return m, cmd
	}

	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filter.SetValue("")
			m.filter.Blur()
			if m.screen == screenLogs {
				m.logSearch = ""
				m.rebuildLogView()
			} else if m.screen == screenServices {
				m.rebuildServicesView()
			} else if m.screen == screenEC2 {
				m.rebuildEC2View()
			} else if m.screen == screenProfile {
				m.rebuildProfileView()
			} else if m.screen == screenVPC {
				m.rebuildVPCView()
			} else if m.screen == screenSecrets {
				m.rebuildSecretsView()
			} else if m.screen == screenR53Zones {
				m.rebuildR53View()
			} else if m.screen == screenR53Records {
				m.rebuildR53RecordsView()
			} else {
				m.rebuildTable()
			}
			return m, nil
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		if m.screen == screenLogs {
			m.logSearch = m.filter.Value()
			m.rebuildLogView()
		} else if m.screen == screenServices {
			m.servicesCursor = 0
			m.rebuildServicesView()
		} else if m.screen == screenEC2 {
			m.ec2Cursor = 0
			m.rebuildEC2View()
		} else if m.screen == screenProfile {
			m.profilesCursor = 0
			m.rebuildProfileView()
		} else if m.screen == screenVPC {
			m.vpcsCursor = 0
			m.rebuildVPCView()
		} else if m.screen == screenSecrets {
			m.secretsCursor = 0
			m.rebuildSecretsView()
		} else if m.screen == screenR53Zones {
			m.r53ZonesCursor = 0
			m.rebuildR53View()
		} else if m.screen == screenR53Records {
			m.r53RecordsCursor = 0
			m.rebuildR53RecordsView()
		} else {
			m.rebuildTable()
		}
		return m, cmd
	}

	// 목록 화면에서 숫자 키는 index 점프.
	if s := msg.String(); len(s) == 1 && s[0] >= '0' && s[0] <= '9' {
		switch m.screen {
		case screenProfile, screenMode, screenClusters, screenServices, screenEC2,
			screenVPC, screenSecrets, screenR53Zones, screenR53Records:
			return m.handleDigit(s)
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "/":
		if m.canFilter() {
			m.filtering = true
			m.filter.Focus()
			return m, textinput.Blink
		}
	case "esc", "backspace":
		return m.goBack()
	case "r":
		return m, m.refresh()
	case "enter":
		return m.enterSelected()
	case "l":
		return m.openLogs()
	case "t":
		return m.openTaskdef()
	case "m":
		return m.openMonitor()
	case "s":
		return m.openShell()
	case "p":
		if m.screen != screenProfile {
			return m.reselectProfile()
		}
	case "g":
		if m.screen != screenConfig {
			return m.openConfig()
		}
	case "v":
		if m.screen == screenSecretDetail && m.secretDetailName != "" && !m.secretValueRevealed {
			m.status = "값 조회 중... (⚠ 화면·터미널 히스토리에 노출됨)"
			return m, fetchSecretValue(m.clients, m.secretDetailName)
		}
	case "c":
		return m.copyOutput()
	case "f":
		if m.screen == screenLogs && !m.loading && m.err == nil {
			m.logFollow = !m.logFollow
			m.rebuildLogView()
			if m.logFollow {
				return m, tickLogs(m.tailInterval)
			}
			return m, nil
		}
	}

	if m.screen == screenServiceDetail {
		var cmd tea.Cmd
		m.detailView, cmd = m.detailView.Update(msg)
		return m, cmd
	}
	if m.screen == screenLogs {
		var cmd tea.Cmd
		m.logView, cmd = m.logView.Update(msg)
		return m, cmd
	}
	if m.screen == screenTaskdef {
		var cmd tea.Cmd
		m.taskdefView, cmd = m.taskdefView.Update(msg)
		return m, cmd
	}
	if m.screen == screenProfile {
		return m.handleProfileKey(msg)
	}
	if m.screen == screenServices {
		return m.handleServicesKey(msg)
	}
	if m.screen == screenEC2 {
		return m.handleEC2Key(msg)
	}
	if m.screen == screenVPC {
		return m.handleVPCKey(msg)
	}
	if m.screen == screenSecrets {
		return m.handleSecretsKey(msg)
	}
	if m.screen == screenR53Zones {
		return m.handleR53Key(msg)
	}
	if m.screen == screenR53Records {
		return m.handleR53RecordsKey(msg)
	}
	if m.screen == screenVPCDetail {
		var cmd tea.Cmd
		m.vpcDetailView, cmd = m.vpcDetailView.Update(msg)
		return m, cmd
	}
	if m.screen == screenSecretDetail {
		var cmd tea.Cmd
		m.secretDetailView, cmd = m.secretDetailView.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// handleReadOnlyListKey is a shared nav handler for cursor list views
// (VPC/Secrets/R53). Returns updated cursor + whether it changed.
func handleReadOnlyListKey(msg tea.KeyMsg, cursor, n, pageSize int) (int, bool) {
	if n == 0 {
		return cursor, false
	}
	prev := cursor
	if pageSize < 1 {
		pageSize = 5
	}
	switch msg.String() {
	case "up", "k":
		cursor--
	case "down", "j":
		cursor++
	case "pgup":
		cursor -= pageSize
	case "pgdown":
		cursor += pageSize
	case "home", "g":
		cursor = 0
	case "end", "G":
		cursor = n - 1
	default:
		return cursor, false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= n {
		cursor = n - 1
	}
	return cursor, cursor != prev
}

func (m Model) handleVPCKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c, changed := handleReadOnlyListKey(msg, m.vpcsCursor, len(m.vpcsFiltered), m.vpcsView.Height-1)
	if !changed {
		return m, nil
	}
	m.vpcsCursor = c
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.vpcsView.Width = tw
	m.vpcsView.SetContent(renderVPCList(m.vpcsFiltered, m.vpcsCursor, tw))
	ensureCursorLine(&m.vpcsView, m.vpcsCursor+1)
	return m, nil
}

func (m Model) handleSecretsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c, changed := handleReadOnlyListKey(msg, m.secretsCursor, len(m.secretsFiltered), m.secretsView.Height-1)
	if !changed {
		return m, nil
	}
	m.secretsCursor = c
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.secretsView.Width = tw
	m.secretsView.SetContent(renderSecretsList(m.secretsFiltered, m.secretsCursor, tw))
	ensureCursorLine(&m.secretsView, m.secretsCursor+1)
	return m, nil
}

func (m Model) handleR53Key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c, changed := handleReadOnlyListKey(msg, m.r53ZonesCursor, len(m.r53ZonesFiltered), m.r53ZonesView.Height-1)
	if !changed {
		return m, nil
	}
	m.r53ZonesCursor = c
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.r53ZonesView.Width = tw
	m.r53ZonesView.SetContent(renderR53List(m.r53ZonesFiltered, m.r53ZonesCursor, tw))
	ensureCursorLine(&m.r53ZonesView, m.r53ZonesCursor+1)
	return m, nil
}

// handleProfileKey processes navigation keys for the profile picker.
func (m Model) handleProfileKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.profilesFiltered)
	if n == 0 {
		return m, nil
	}
	prev := m.profilesCursor
	page := m.profilesView.Height - 1
	if page < 1 {
		page = 5
	}
	switch msg.String() {
	case "up", "k":
		m.profilesCursor--
	case "down", "j":
		m.profilesCursor++
	case "pgup":
		m.profilesCursor -= page
	case "pgdown":
		m.profilesCursor += page
	case "home", "g":
		m.profilesCursor = 0
	case "end", "G":
		m.profilesCursor = n - 1
	default:
		return m, nil
	}
	if m.profilesCursor < 0 {
		m.profilesCursor = 0
	}
	if m.profilesCursor >= n {
		m.profilesCursor = n - 1
	}
	if m.profilesCursor == prev {
		return m, nil
	}
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.profilesView.Width = tw
	m.profilesView.SetContent(renderProfileList(m.profilesFiltered, m.profilesCursor, tw))
	m.ensureProfileCursorVisible()
	return m, nil
}

// handleEC2Key processes navigation keys for the EC2 list.
func (m Model) handleEC2Key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.ec2Filtered)
	if n == 0 {
		return m, nil
	}
	prev := m.ec2Cursor
	page := m.ec2View.Height - 1
	if page < 1 {
		page = 5
	}
	switch msg.String() {
	case "up", "k":
		m.ec2Cursor--
	case "down", "j":
		m.ec2Cursor++
	case "pgup":
		m.ec2Cursor -= page
	case "pgdown":
		m.ec2Cursor += page
	case "home", "g":
		m.ec2Cursor = 0
	case "end", "G":
		m.ec2Cursor = n - 1
	default:
		return m, nil
	}
	if m.ec2Cursor < 0 {
		m.ec2Cursor = 0
	}
	if m.ec2Cursor >= n {
		m.ec2Cursor = n - 1
	}
	if m.ec2Cursor == prev {
		return m, nil
	}
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.ec2View.Width = tw
	m.ec2View.SetContent(renderEC2List(m.ec2Filtered, m.ec2Cursor, tw))
	m.ensureEC2CursorVisible()
	return m, nil
}

// handleServicesKey processes navigation keys for the services list (custom
// renderer, not bubbles/table). Also fires monitor refetch when the cursor
// selects a different service.
func (m Model) handleServicesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.servicesFiltered)
	if n == 0 {
		return m, nil
	}
	prev := m.servicesCursor
	page := m.servicesView.Height - 1
	if page < 1 {
		page = 5
	}
	switch msg.String() {
	case "up", "k":
		m.servicesCursor--
	case "down", "j":
		m.servicesCursor++
	case "pgup":
		m.servicesCursor -= page
	case "pgdown":
		m.servicesCursor += page
	case "home", "g":
		m.servicesCursor = 0
	case "end", "G":
		m.servicesCursor = n - 1
	default:
		return m, nil
	}
	if m.servicesCursor < 0 {
		m.servicesCursor = 0
	}
	if m.servicesCursor >= n {
		m.servicesCursor = n - 1
	}
	if m.servicesCursor == prev {
		return m, nil
	}
	// rerender to move highlight + adjust viewport
	tw := m.width
	if m.monitorVisible() {
		tw = m.width - m.monitorPaneW()
	}
	if tw < 30 {
		tw = 30
	}
	m.servicesView.Width = tw
	m.servicesView.SetContent(renderServicesList(m.servicesFiltered, m.servicesCursor, tw))
	m.ensureCursorVisible()

	svc := m.servicesFiltered[m.servicesCursor].Name
	if svc != m.currentMonitorSvc {
		m.currentMonitorSvc = svc
		m.monitorContent = ""
		return m, fetchMonitor(m.clients, m.cluster, svc)
	}
	return m, nil
}

// copyOutput copies the current screen's payload to the system clipboard.
// Logs use log lines (respecting active search filter); detail/taskdef/monitor
// use the stored colorized text with ANSI stripped.
func (m Model) copyOutput() (tea.Model, tea.Cmd) {
	text := m.copyText()
	if text == "" {
		m.status = "복사할 내용이 없습니다."
		return m, nil
	}
	msg, err := copyToClipboard(text)
	if err != nil {
		m.status = "복사 실패: " + err.Error()
	} else {
		m.status = msg
	}
	return m, nil
}

func (m Model) copyText() string {
	switch m.screen {
	case screenLogs:
		if len(m.logLines) == 0 {
			return ""
		}
		needle := strings.ToLower(m.logSearch)
		if needle == "" {
			return strings.Join(m.logLines, "\n")
		}
		var out []string
		for _, ln := range m.logLines {
			if strings.Contains(strings.ToLower(ln), needle) {
				out = append(out, ln)
			}
		}
		return strings.Join(out, "\n")
	case screenServiceDetail:
		return stripANSI(m.detailRaw)
	case screenTaskdef:
		return stripANSI(m.taskdefRaw)
	case screenMonitor:
		return stripANSI(m.monitorContent)
	case screenVPCDetail:
		return stripANSI(m.vpcDetailRaw)
	case screenSecretDetail:
		return stripANSI(m.secretDetailRaw)
	}
	return ""
}

// openConfig jumps to the settings screen. Independent of profile — usable
// from any screen (even before a profile is selected).
func (m Model) openConfig() (tea.Model, tea.Cmd) {
	m.status = "설정 파일: " + config.Path()
	m.err = nil
	return m.push(screenConfig), nil
}

// saveAndApply persists cfg to disk and updates any runtime fields derived
// from it. Called after every edit or toggle on the config screen.
func (m *Model) saveAndApply(key string) {
	if err := config.Save(m.cfg); err != nil {
		m.status = "저장 실패: " + err.Error()
		return
	}
	switch key {
	case "region":
		m.region = m.cfg.Region
	case "command":
		m.command = m.cfg.Command
		if m.command == "" {
			m.command = "/bin/sh"
		}
	case "tail_interval_sec":
		if d := time.Duration(m.cfg.TailIntervalSec) * time.Second; d >= time.Second {
			m.tailInterval = d
		}
	case "monitor_interval_sec":
		if d := time.Duration(m.cfg.MonitorIntervalSec) * time.Second; d >= 5*time.Second {
			m.monitorInterval = d
		}
	case "log_enabled", "log_level", "log_path":
		config.SetupLogging(m.cfg, m.version)
	}
	m.status = "저장됨 (일부 항목은 재선택/재시작 후 적용)"
}

// openShell dispatches an ECS execute-command shell for the service selected
// on the current screen. Resolves the first RUNNING task + first container
// asynchronously, then tea.ExecProcess suspends the TUI while the shell runs.
func (m Model) openShell() (tea.Model, tea.Cmd) {
	var svc string
	switch m.screen {
	case screenServices:
		row, ok := m.currentServiceRow()
		if !ok {
			return m, nil
		}
		svc = row.Name
		m.service = svc
	case screenServiceDetail:
		svc = m.service
	default:
		return m, nil
	}
	m.loading = true
	m.status = "running task 조회중..."
	return m, fetchShellTarget(m.clients, m.profile, m.region, m.cluster, svc, m.command)
}

// reselectProfile jumps back to the profile picker, dropping any downstream
// state that was tied to the previously selected profile. Nav stack is
// cleared so Esc from the resulting mode screen exits (rather than
// resurrecting stale screens from another profile).
func (m Model) reselectProfile() (tea.Model, tea.Cmd) {
	m.stack = nil
	m.screen = screenProfile
	m.cluster = ""
	m.clusterStatus = ""
	m.service = ""
	m.clusters = nil
	m.services = nil
	m.ec2 = nil
	m.err = nil
	m.status = ""
	m.filter.SetValue("")
	m.filtering = false
	m.logLines = nil
	m.logFollow = false
	m.logSearch = ""
	m.logContainer = ""
	m.taskdefARN = ""
	m.monitorContent = ""
	m.loading = true
	m.rebuildTable()
	return m, fetchProfiles()
}

// openMonitor opens the live monitor for the service selected on the current
// screen and kicks off the periodic refresh tick.
func (m Model) openMonitor() (tea.Model, tea.Cmd) {
	var svc string
	switch m.screen {
	case screenServices:
		row, ok := m.currentServiceRow()
		if !ok {
			return m, nil
		}
		svc = row.Name
		m.service = svc
	case screenServiceDetail:
		svc = m.service
	default:
		return m, nil
	}
	m.loading = true
	m.monitorContent = ""
	return m.push(screenMonitor), tea.Batch(
		fetchMonitor(m.clients, m.cluster, svc),
		tickMonitor(m.monitorInterval),
	)
}

// openTaskdef opens the task-definition JSON viewer for the service selected
// on the current screen. Mirrors openLogs.
func (m Model) openTaskdef() (tea.Model, tea.Cmd) {
	var svc string
	switch m.screen {
	case screenServices:
		row, ok := m.currentServiceRow()
		if !ok {
			return m, nil
		}
		svc = row.Name
		m.service = svc
	case screenServiceDetail:
		svc = m.service
	default:
		return m, nil
	}
	m.loading = true
	m.taskdefARN = ""
	m.taskdefView.SetContent("")
	return m.push(screenTaskdef), fetchTaskdef(m.clients, m.cluster, svc)
}

// openLogs opens the log viewer for the service selected on the current screen.
func (m Model) openLogs() (tea.Model, tea.Cmd) {
	var svc string
	switch m.screen {
	case screenServices:
		row, ok := m.currentServiceRow()
		if !ok {
			return m, nil
		}
		svc = row.Name
		m.service = svc
	case screenServiceDetail:
		svc = m.service
	default:
		return m, nil
	}
	m.loading = true
	m.logLines = nil
	m.logFollow = false
	m.logSearch = ""
	m.filter.SetValue("")
	return m.push(screenLogs), fetchLogs(m.clients, m.cluster, svc, m.region)
}

// enterSelected acts on Enter based on current screen.
func (m Model) enterSelected() (tea.Model, tea.Cmd) {
	if m.loading {
		return m, nil
	}

	// 커스텀 렌더러를 쓰는 화면은 자체 accessor 사용.
	switch m.screen {
	case screenProfile:
		row, ok := m.currentProfileRow()
		if !ok {
			return m, nil
		}
		m.profile = row.Name
		return m.push(screenMode), loadClients(m.profile, m.region)
	}

	// bubbles/table 기반 화면들. 커스텀 렌더러 화면들은 table 을 안 쓰므로 예외.
	row := m.table.SelectedRow()
	switch m.screen {
	case screenServices, screenEC2, screenConfig,
		screenVPC, screenSecrets, screenR53Zones:
		// 커스텀 accessor 를 아래 케이스에서 사용 → row 비어도 통과
	default:
		if len(row) == 0 {
			return m, nil
		}
	}
	pick := ""
	if len(row) > 0 {
		pick = row[0]
	}

	switch m.screen {
	case screenMode:
		if m.clients == nil {
			m.status = "AWS 클라이언트 로드 중..."
			return m, nil
		}
		m.loading = true
		switch {
		case strings.HasPrefix(pick, "ECS"):
			return m.push(screenClusters), fetchClusters(m.clients)
		case strings.HasPrefix(pick, "EC2"):
			return m.push(screenEC2), fetchEC2(m.clients)
		case strings.HasPrefix(pick, "VPC"):
			return m.push(screenVPC), fetchVPCs(m.clients)
		case strings.HasPrefix(pick, "Secrets"):
			return m.push(screenSecrets), fetchSecrets(m.clients)
		case strings.HasPrefix(pick, "Route 53"):
			return m.push(screenR53Zones), fetchR53Zones(m.clients)
		}
		m.loading = false
		return m, nil

	case screenClusters:
		// row[0] 은 인덱스, row[1] 이 실제 클러스터 이름.
		row := m.table.SelectedRow()
		if len(row) < 2 {
			return m, nil
		}
		name := row[1]
		m.cluster = name
		m.clusterStatus = ""
		for _, c := range m.clusters {
			if c.Name == name {
				m.clusterStatus = c.Status
				break
			}
		}
		m.loading = true
		return m.push(screenServices), fetchServices(m.clients, m.cluster)

	case screenServices:
		row, ok := m.currentServiceRow()
		if !ok {
			return m, nil
		}
		m.service = row.Name
		m.loading = true
		m.detailView.SetContent("")
		return m.push(screenServiceDetail),
			fetchServiceDetail(m.clients, m.cluster, m.clusterStatus, m.service)

	case screenConfig:
		idx := m.table.Cursor()
		if idx < 0 || idx >= len(settings) {
			return m, nil
		}
		s := settings[idx]
		if s.typ == settingBool {
			s.toggle(&m.cfg)
			m.saveAndApply(s.key)
			m.rebuildTable()
			return m, nil
		}
		m.editingIdx = idx
		m.editInput.Prompt = s.label + ": "
		m.editInput.SetValue(s.getStr(m.cfg))
		m.editInput.Focus()
		return m, textinput.Blink

	case screenVPC:
		v, ok := m.currentVPCRow()
		if !ok {
			return m, nil
		}
		m.loading = true
		m.vpcDetailView.SetContent("")
		return m.push(screenVPCDetail), fetchVPCDetail(m.clients, v)

	case screenSecrets:
		s, ok := m.currentSecretRow()
		if !ok {
			return m, nil
		}
		m.secretDetailName = s.Name
		m.secretDetail = nil
		m.secretValueRevealed = false
		m.secretValue = ""
		m.secretValueErr = nil
		m.loading = true
		m.secretDetailView.SetContent("")
		return m.push(screenSecretDetail), fetchSecretDetail(m.clients, s.Name)

	case screenR53Zones:
		z, ok := m.currentR53ZoneRow()
		if !ok {
			return m, nil
		}
		m.r53CurrentZoneID = z.ID
		m.r53CurrentZoneName = z.Name
		m.loading = true
		return m.push(screenR53Records), fetchR53Records(m.clients, z.ID)

	case screenEC2:
		i, ok := m.currentEC2Row()
		if !ok {
			return m, nil
		}
		if i.State != "running" {
			m.status = fmt.Sprintf("경고: %s state=%s (SSM 불가)", i.ID, i.State)
			return m, nil
		}
		user := i.OSUser
		if user == "" {
			user = "ssm-user"
		}
		if i.SSM != "Online" {
			m.status = fmt.Sprintf("경고: %s SSM=%s (연결 실패 가능, OS user=%s)", i.ID, i.SSM, user)
		} else {
			m.status = fmt.Sprintf("SSM 세션 접속: %s (SSM=ssm-user · OS user=%s)", i.ID, user)
		}
		return m, execSSM(m.profile, m.region, i.ID, i.Name, user)
	}
	return m, nil
}

func (m Model) goBack() (tea.Model, tea.Cmd) {
	if len(m.stack) == 0 {
		return m, tea.Quit
	}
	prev := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	m.screen = prev
	m.err = nil
	m.status = ""
	m.filter.SetValue("")
	m.rebuildTable()
	return m, nil
}

func (m Model) push(next screen) Model {
	m.stack = append(m.stack, m.screen)
	m.screen = next
	m.filter.SetValue("")
	m.err = nil
	m.status = ""
	m.rebuildTable()
	return m
}

func (m Model) refresh() tea.Cmd {
	if m.clients == nil {
		return nil
	}
	switch m.screen {
	case screenProfile:
		return fetchProfiles()
	case screenClusters:
		return fetchClusters(m.clients)
	case screenServices:
		return fetchServices(m.clients, m.cluster)
	case screenEC2:
		return fetchEC2(m.clients)
	case screenServiceDetail:
		return fetchServiceDetail(m.clients, m.cluster, m.clusterStatus, m.service)
	case screenLogs:
		return fetchLogs(m.clients, m.cluster, m.service, m.region)
	case screenTaskdef:
		return fetchTaskdef(m.clients, m.cluster, m.service)
	case screenMonitor:
		return fetchMonitor(m.clients, m.cluster, m.service)
	case screenVPC:
		return fetchVPCs(m.clients)
	case screenSecrets:
		return fetchSecrets(m.clients)
	case screenR53Zones:
		return fetchR53Zones(m.clients)
	case screenVPCDetail:
		return fetchVPCDetail(m.clients, awsx.VPCRow{
			ID: m.vpcDetailID, Name: m.vpcDetailName,
		})
	case screenSecretDetail:
		if m.secretDetailName != "" {
			return fetchSecretDetail(m.clients, m.secretDetailName)
		}
	case screenR53Records:
		if m.r53CurrentZoneID != "" {
			return fetchR53Records(m.clients, m.r53CurrentZoneID)
		}
	}
	return nil
}

func (m Model) canFilter() bool {
	switch m.screen {
	case screenProfile, screenClusters, screenServices, screenEC2, screenLogs,
		screenVPC, screenSecrets, screenR53Zones, screenR53Records:
		return true
	}
	return false
}

// rebuildTable rebuilds table columns/rows for the current screen and filter.
func (m *Model) rebuildTable() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	width := m.width
	if width < 20 {
		width = 80
	}

	var cols []table.Column
	var rows []table.Row

	switch m.screen {
	case screenProfile:
		m.rebuildProfileView()
		return

	case screenMode:
		cols = []table.Column{{Title: "MODE", Width: width - 4}}
		rows = []table.Row{
			{"ECS (cluster/service)"},
			{"EC2 (SSM)"},
			{"VPC"},
			{"Secrets Manager"},
			{"Route 53"},
		}

	case screenClusters:
		nameW := width - 4 - idxColW - 12 - 12 - 12 - 12
		if nameW < 20 {
			nameW = 20
		}
		cols = []table.Column{
			{Title: "#", Width: idxColW},
			{Title: "CLUSTER", Width: nameW},
			{Title: "STATUS", Width: 12},
			{Title: "ACTIVE", Width: 12},
			{Title: "RUNNING", Width: 12},
			{Title: "PENDING", Width: 12},
		}
		idx := 0
		for _, c := range m.clusters {
			if q != "" && !strings.Contains(strings.ToLower(c.Name), q) {
				continue
			}
			idx++
			rows = append(rows, table.Row{
				fmt.Sprintf("%d", idx),
				c.Name, c.Status,
				fmt.Sprintf("%d", c.Active),
				fmt.Sprintf("%d", c.Running),
				fmt.Sprintf("%d", c.Pending),
			})
		}

	case screenServices:
		// 서비스 화면은 커스텀 렌더러가 서비스 리스트를 그리므로
		// bubbles/table 은 사용하지 않는다. rebuildServicesView 로 위임.
		m.rebuildServicesView()
		return

	case screenConfig:
		labelW := width - 4 - 22 - 22
		if labelW < 30 {
			labelW = 30
		}
		cols = []table.Column{
			{Title: "SETTING", Width: labelW},
			{Title: "VALUE", Width: 22},
			{Title: "DEFAULT", Width: 22},
		}
		for _, s := range settings {
			cur := s.getStr(m.cfg)
			def := s.getStr(defaultCfg)
			rows = append(rows, table.Row{s.label, cur, def})
		}

	case screenEC2:
		m.rebuildEC2View()
		return
	}

	h := m.height - 4
	if h < 5 {
		h = 20
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(h),
	)
	st := table.DefaultStyles()
	st.Header = st.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true).
		Foreground(lipgloss.Color("51"))
	st.Selected = st.Selected.
		Foreground(lipgloss.Color("232")).
		Background(lipgloss.Color("39")).
		Bold(true)
	t.SetStyles(st)
	m.table = t
}

func (m Model) View() string {
	var b strings.Builder

	// Top bar: breadcrumb + version
	crumb := m.breadcrumb()
	version := "awscx-go " + m.version
	pad := m.width - lipgloss.Width(crumb) - lipgloss.Width(version) - 2
	if pad < 1 {
		pad = 1
	}
	top := styleTop.Render(crumb + strings.Repeat(" ", pad) + version)
	b.WriteString(top)
	b.WriteString("\n")

	// Filter line (when active)
	if m.filtering {
		b.WriteString(m.filter.View())
		b.WriteString("\n")
	}
	// Config edit input (when active)
	if m.editingIdx >= 0 {
		b.WriteString(m.editInput.View())
		b.WriteString("\n")
	}

	// Body
	switch {
	case m.loading:
		b.WriteString(m.spinner.View() + " 로딩중...\n")
	case m.err != nil:
		b.WriteString(styleErr.Render("ERROR: "+m.err.Error()) + "\n")
	case m.screen == screenServiceDetail:
		b.WriteString(m.detailView.View() + "\n")
	case m.screen == screenLogs:
		b.WriteString(m.logView.View() + "\n")
	case m.screen == screenTaskdef:
		b.WriteString(m.taskdefView.View() + "\n")
	case m.screen == screenMonitor:
		b.WriteString(m.monitorContent + "\n")
	case m.screen == screenServices && m.monitorVisible():
		paneW := m.monitorPaneW()
		innerW := paneW - 3
		title := styleMonTitle.Render("모니터  " + m.currentMonitorSvc)
		body := m.monitorContent
		if body == "" {
			body = dimStyle.Render("(조회 중 · 커서 이동 시 자동 갱신)")
		}
		content := wrapANSI(title+"\n"+body, innerW)
		pane := styleMonPane.Width(paneW).Render(content)
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, m.servicesView.View(), pane))
		b.WriteString("\n")
	case m.screen == screenServices:
		b.WriteString(m.servicesView.View() + "\n")
	case m.screen == screenEC2:
		b.WriteString(m.ec2View.View() + "\n")
	case m.screen == screenProfile:
		b.WriteString(m.profilesView.View() + "\n")
	case m.screen == screenVPC:
		b.WriteString(m.vpcsView.View() + "\n")
	case m.screen == screenSecrets:
		b.WriteString(m.secretsView.View() + "\n")
	case m.screen == screenR53Zones:
		b.WriteString(m.r53ZonesView.View() + "\n")
	case m.screen == screenVPCDetail:
		b.WriteString(m.vpcDetailView.View() + "\n")
	case m.screen == screenSecretDetail:
		b.WriteString(m.secretDetailView.View() + "\n")
	case m.screen == screenR53Records:
		b.WriteString(m.r53RecordsView.View() + "\n")
	default:
		b.WriteString(m.table.View() + "\n")
	}

	// Status / help
	help := m.helpText()
	status := m.status
	if status != "" {
		b.WriteString(styleStatus.Render(status) + "\n")
	}
	b.WriteString(styleHelp.Render(help))
	return b.String()
}

func (m Model) breadcrumb() string {
	parts := []string{"awscx-go"}
	if m.profile != "" {
		parts = append(parts, m.profile)
	}
	switch m.screen {
	case screenProfile:
		parts = append(parts, fmt.Sprintf("profile[%d]", len(m.profilesFiltered)))
	case screenMode:
		parts = append(parts, "mode")
	case screenClusters:
		parts = append(parts, fmt.Sprintf("ecs · clusters[%d]", len(m.clusters)))
	case screenServices:
		parts = append(parts, "ecs", fmt.Sprintf("%s · services[%d]", m.cluster, len(m.servicesFiltered)))
	case screenEC2:
		parts = append(parts, fmt.Sprintf("ec2[%d]", len(m.ec2Filtered)))
	case screenServiceDetail:
		parts = append(parts, "ecs", m.cluster, m.service, "status")
	case screenLogs:
		parts = append(parts, "ecs", m.cluster, m.service, "logs")
		if m.logContainer != "" {
			parts = append(parts, m.logContainer)
		}
	case screenTaskdef:
		parts = append(parts, "ecs", m.cluster, m.service, "taskdef")
		if m.taskdefARN != "" {
			parts = append(parts, awsx.ShortARN(m.taskdefARN))
		}
	case screenMonitor:
		parts = append(parts, "ecs", m.cluster, m.service, "monitor")
	case screenConfig:
		parts = append(parts, "config")
	case screenVPC:
		parts = append(parts, fmt.Sprintf("vpc[%d]", len(m.vpcsFiltered)))
	case screenVPCDetail:
		nm := m.vpcDetailID
		if m.vpcDetailName != "" {
			nm = m.vpcDetailName
		}
		parts = append(parts, "vpc", nm)
	case screenSecrets:
		parts = append(parts, fmt.Sprintf("secrets[%d]", len(m.secretsFiltered)))
	case screenSecretDetail:
		parts = append(parts, "secrets", m.secretDetailName)
	case screenR53Zones:
		parts = append(parts, fmt.Sprintf("route53[%d]", len(m.r53ZonesFiltered)))
	case screenR53Records:
		parts = append(parts, "route53",
			fmt.Sprintf("%s · records[%d]", strings.TrimSuffix(m.r53CurrentZoneName, "."), len(m.r53RecordsFiltered)))
	}
	return strings.Join(parts, " › ")
}

func (m Model) helpText() string {
	if m.editingIdx >= 0 {
		return "설정 편집 · Enter=저장 · Esc=취소"
	}
	if m.filtering {
		if m.screen == screenLogs {
			return "로그 검색 · Enter=적용 · Esc=취소"
		}
		return "필터 입력중 · Enter=적용 · Esc=취소"
	}
	if m.screen == screenServiceDetail || m.screen == screenTaskdef || m.screen == screenVPCDetail {
		return "↑/↓ · PgUp/PgDn 스크롤 · r=새로고침 · c=복사 · p=프로파일 · Esc/Backspace=뒤로 · q=종료"
	}
	if m.screen == screenSecretDetail {
		vhint := "v=값조회"
		if m.secretValueRevealed {
			vhint = "값 노출됨"
		}
		return "↑/↓ · PgUp/PgDn · " + vhint + " · r=새로고침 · c=복사 · p=프로파일 · Esc=뒤로 · q=종료"
	}
	if m.screen == screenLogs {
		tail := "f=tail"
		if m.logFollow {
			tail = "f=tail중지"
		}
		total := len(m.logLines)
		info := fmt.Sprintf("%d줄", total)
		if m.logSearch != "" {
			info = fmt.Sprintf("검색 '%s'", m.logSearch)
		}
		return info + " · " + tail + " · /=검색 · r=새로고침 · c=복사 · p=프로파일 · ↑/↓/PgUp/PgDn · Esc=뒤로 · q=종료"
	}
	if m.screen == screenServices {
		return "Enter=상태 · s=shell · l=로그 · t=taskdef · m=모니터 · /=필터 · r=새로고침 · p=프로파일 · Esc=뒤로 · q=종료"
	}
	if m.screen == screenEC2 {
		return "Enter=SSM 접속 · /=필터 · r=새로고침 · p=프로파일 · Esc=뒤로 · q=종료"
	}
	if m.screen == screenMonitor {
		return fmt.Sprintf("자동 갱신 %ds · r=지금 새로고침 · c=복사 · p=프로파일 · Esc=뒤로 · q=종료",
			int(m.monitorInterval/time.Second))
	}
	if m.screen == screenProfile || m.screen == screenMode {
		return "↑/↓ 이동 · Enter=선택 · /=필터 · r=새로고침 · g=설정 · Esc=뒤로 · q=종료"
	}
	if m.screen == screenConfig {
		return "↑/↓ 이동 · Enter=편집/토글 · p=프로파일 · Esc=뒤로 · q=종료"
	}
	return "↑/↓ 이동 · Enter=선택 · /=필터 · r=새로고침 · p=프로파일 · g=설정 · Esc/Backspace=뒤로 · q=종료"
}

// rebuildTaskdefView (re)wraps the stored colorized JSON to the current
// viewport width. Cheap enough to run on every resize.
func (m *Model) rebuildTaskdefView() {
	if m.taskdefRaw == "" {
		m.taskdefView.SetContent("")
		return
	}
	width := m.taskdefView.Width
	if width < 20 {
		width = 80
	}
	m.taskdefView.SetContent(wrapANSI(m.taskdefRaw, width))
}

// rebuildServicesView refreshes the filtered subset + viewport content for
// the services screen. Cursor is clamped to the filtered length. servicesView
// width is set here (not on WindowSizeMsg) because it depends on whether the
// monitor pane is joined on the right.
func (m *Model) rebuildServicesView() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	filtered := m.services[:0:0]
	for _, s := range m.services {
		if q != "" && !strings.Contains(strings.ToLower(s.Name), q) {
			continue
		}
		filtered = append(filtered, s)
	}
	m.servicesFiltered = filtered
	if m.servicesCursor >= len(filtered) {
		m.servicesCursor = len(filtered) - 1
	}
	if m.servicesCursor < 0 {
		m.servicesCursor = 0
	}

	tw := m.width
	if tw < 40 {
		tw = 80
	}
	if m.monitorVisible() {
		tw = m.width - m.monitorPaneW()
		if tw < 30 {
			tw = 30
		}
	}
	m.servicesView.Width = tw
	content := renderServicesList(m.servicesFiltered, m.servicesCursor, tw)
	m.servicesView.SetContent(content)
	m.ensureCursorVisible()
}

// ensureCursorVisible adjusts the viewport YOffset so the current cursor row
// is on-screen. Header is line 0, so cursor row is at content line (cursor+1).
func (m *Model) ensureCursorVisible() {
	line := m.servicesCursor + 1
	top := m.servicesView.YOffset
	bot := top + m.servicesView.Height - 1
	if line < top {
		m.servicesView.YOffset = line
	} else if line > bot {
		m.servicesView.YOffset = line - m.servicesView.Height + 1
	}
}

// --- VPC / Secrets / Route53 : same pattern as services/ec2 -------------

func (m *Model) rebuildVPCView() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	filtered := m.vpcs[:0:0]
	for _, v := range m.vpcs {
		hay := strings.ToLower(v.Name + " " + v.ID + " " + v.CIDR + " " + v.State)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		filtered = append(filtered, v)
	}
	m.vpcsFiltered = filtered
	if m.vpcsCursor >= len(filtered) {
		m.vpcsCursor = len(filtered) - 1
	}
	if m.vpcsCursor < 0 {
		m.vpcsCursor = 0
	}
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.vpcsView.Width = tw
	m.vpcsView.SetContent(renderVPCList(m.vpcsFiltered, m.vpcsCursor, tw))
	ensureCursorLine(&m.vpcsView, m.vpcsCursor+1)
}

func (m *Model) rebuildSecretsView() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	filtered := m.secrets[:0:0]
	for _, s := range m.secrets {
		hay := strings.ToLower(s.Name + " " + s.Description)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		filtered = append(filtered, s)
	}
	m.secretsFiltered = filtered
	if m.secretsCursor >= len(filtered) {
		m.secretsCursor = len(filtered) - 1
	}
	if m.secretsCursor < 0 {
		m.secretsCursor = 0
	}
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.secretsView.Width = tw
	m.secretsView.SetContent(renderSecretsList(m.secretsFiltered, m.secretsCursor, tw))
	ensureCursorLine(&m.secretsView, m.secretsCursor+1)
}

func (m *Model) rebuildR53View() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	filtered := m.r53Zones[:0:0]
	for _, z := range m.r53Zones {
		hay := strings.ToLower(z.Name + " " + z.ID + " " + z.Comment)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		filtered = append(filtered, z)
	}
	m.r53ZonesFiltered = filtered
	if m.r53ZonesCursor >= len(filtered) {
		m.r53ZonesCursor = len(filtered) - 1
	}
	if m.r53ZonesCursor < 0 {
		m.r53ZonesCursor = 0
	}
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.r53ZonesView.Width = tw
	m.r53ZonesView.SetContent(renderR53List(m.r53ZonesFiltered, m.r53ZonesCursor, tw))
	ensureCursorLine(&m.r53ZonesView, m.r53ZonesCursor+1)
}

// ensureCursorLine keeps `line` (1-based content line index) visible in vp.
func ensureCursorLine(vp *viewport.Model, line int) {
	top := vp.YOffset
	bot := top + vp.Height - 1
	if line < top {
		vp.YOffset = line
	} else if line > bot {
		vp.YOffset = line - vp.Height + 1
	}
}

// rebuildVPCDetailView rewraps stored raw VPC detail to current viewport width.
func (m *Model) rebuildVPCDetailView() {
	if m.vpcDetailRaw == "" {
		m.vpcDetailView.SetContent("")
		return
	}
	w := m.vpcDetailView.Width
	if w < 20 {
		w = 80
	}
	m.vpcDetailView.SetContent(wrapANSI(m.vpcDetailRaw, w))
}

// rebuildSecretDetailView rebuilds + rewraps secret detail (includes reveal state).
func (m *Model) rebuildSecretDetailView() {
	m.secretDetailRaw = renderSecretDetail(m.secretDetail, m.secretValueRevealed, m.secretValue, m.secretValueErr)
	w := m.secretDetailView.Width
	if w < 20 {
		w = 80
	}
	m.secretDetailView.SetContent(wrapANSI(m.secretDetailRaw, w))
}

// rebuildR53RecordsView rebuilds the filtered records list.
func (m *Model) rebuildR53RecordsView() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	filtered := m.r53Records[:0:0]
	for _, r := range m.r53Records {
		hay := strings.ToLower(r.Name + " " + r.Type + " " + strings.Join(r.Values, " ") + " " + r.AliasDNS)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		filtered = append(filtered, r)
	}
	m.r53RecordsFiltered = filtered
	if m.r53RecordsCursor >= len(filtered) {
		m.r53RecordsCursor = len(filtered) - 1
	}
	if m.r53RecordsCursor < 0 {
		m.r53RecordsCursor = 0
	}
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.r53RecordsView.Width = tw
	m.r53RecordsView.SetContent(renderR53RecordList(m.r53RecordsFiltered, m.r53RecordsCursor, tw))
	ensureCursorLine(&m.r53RecordsView, m.r53RecordsCursor+1)
}

func (m Model) handleR53RecordsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c, changed := handleReadOnlyListKey(msg, m.r53RecordsCursor, len(m.r53RecordsFiltered), m.r53RecordsView.Height-1)
	if !changed {
		return m, nil
	}
	m.r53RecordsCursor = c
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.r53RecordsView.Width = tw
	m.r53RecordsView.SetContent(renderR53RecordList(m.r53RecordsFiltered, m.r53RecordsCursor, tw))
	ensureCursorLine(&m.r53RecordsView, m.r53RecordsCursor+1)
	return m, nil
}

func (m Model) currentR53ZoneRow() (awsx.R53ZoneRow, bool) {
	if m.r53ZonesCursor < 0 || m.r53ZonesCursor >= len(m.r53ZonesFiltered) {
		return awsx.R53ZoneRow{}, false
	}
	return m.r53ZonesFiltered[m.r53ZonesCursor], true
}

func (m Model) currentVPCRow() (awsx.VPCRow, bool) {
	if m.vpcsCursor < 0 || m.vpcsCursor >= len(m.vpcsFiltered) {
		return awsx.VPCRow{}, false
	}
	return m.vpcsFiltered[m.vpcsCursor], true
}

func (m Model) currentSecretRow() (awsx.SecretRow, bool) {
	if m.secretsCursor < 0 || m.secretsCursor >= len(m.secretsFiltered) {
		return awsx.SecretRow{}, false
	}
	return m.secretsFiltered[m.secretsCursor], true
}

// rebuildProfileView refreshes the filtered subset + viewport for the
// profile picker. Applies config.ProfilePrefix (regex) as a pre-filter, then
// the interactive `/` filter on top.
func (m *Model) rebuildProfileView() {
	var re *regexp.Regexp
	if pat := strings.TrimSpace(m.cfg.ProfilePrefix); pat != "" {
		r, err := regexp.Compile(pat)
		if err != nil {
			m.status = "profile_prefix regex 오류: " + err.Error()
		} else {
			re = r
		}
	}
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	filtered := m.profiles[:0:0]
	for _, p := range m.profiles {
		if re != nil && !re.MatchString(p.Name) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p.Name), q) {
			continue
		}
		filtered = append(filtered, p)
	}
	m.profilesFiltered = filtered
	if m.profilesCursor >= len(filtered) {
		m.profilesCursor = len(filtered) - 1
	}
	if m.profilesCursor < 0 {
		m.profilesCursor = 0
	}
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.profilesView.Width = tw
	m.profilesView.SetContent(renderProfileList(m.profilesFiltered, m.profilesCursor, tw))
	m.ensureProfileCursorVisible()
}

func (m *Model) ensureProfileCursorVisible() {
	line := m.profilesCursor + 1
	top := m.profilesView.YOffset
	bot := top + m.profilesView.Height - 1
	if line < top {
		m.profilesView.YOffset = line
	} else if line > bot {
		m.profilesView.YOffset = line - m.profilesView.Height + 1
	}
}

func (m Model) currentProfileRow() (awsx.ProfileToken, bool) {
	if m.profilesCursor < 0 || m.profilesCursor >= len(m.profilesFiltered) {
		return awsx.ProfileToken{}, false
	}
	return m.profilesFiltered[m.profilesCursor], true
}

// rebuildEC2View refreshes the filtered subset + viewport content for the
// EC2 screen. Same pattern as rebuildServicesView.
func (m *Model) rebuildEC2View() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	filtered := m.ec2[:0:0]
	for _, i := range m.ec2 {
		hay := strings.ToLower(i.Name + " " + i.ID + " " + i.IP + " " + i.PublicIP + " " + i.State)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		filtered = append(filtered, i)
	}
	m.ec2Filtered = filtered
	if m.ec2Cursor >= len(filtered) {
		m.ec2Cursor = len(filtered) - 1
	}
	if m.ec2Cursor < 0 {
		m.ec2Cursor = 0
	}
	tw := m.width
	if tw < 40 {
		tw = 80
	}
	m.ec2View.Width = tw
	m.ec2View.SetContent(renderEC2List(m.ec2Filtered, m.ec2Cursor, tw))
	m.ensureEC2CursorVisible()
}

func (m *Model) ensureEC2CursorVisible() {
	line := m.ec2Cursor + 1
	top := m.ec2View.YOffset
	bot := top + m.ec2View.Height - 1
	if line < top {
		m.ec2View.YOffset = line
	} else if line > bot {
		m.ec2View.YOffset = line - m.ec2View.Height + 1
	}
}

func (m Model) currentEC2Row() (awsx.EC2Instance, bool) {
	if m.ec2Cursor < 0 || m.ec2Cursor >= len(m.ec2Filtered) {
		return awsx.EC2Instance{}, false
	}
	return m.ec2Filtered[m.ec2Cursor], true
}

// currentServiceRow returns the row at the cursor in the filtered view.
// Returns nil-ish when list is empty.
func (m Model) currentServiceRow() (awsx.ServiceRow, bool) {
	if m.servicesCursor < 0 || m.servicesCursor >= len(m.servicesFiltered) {
		return awsx.ServiceRow{}, false
	}
	return m.servicesFiltered[m.servicesCursor], true
}

// rebuildDetailView rewraps the stored colorized service-detail text to the
// current viewport width. Same pattern as rebuildTaskdefView.
func (m *Model) rebuildDetailView() {
	if m.detailRaw == "" {
		m.detailView.SetContent("")
		return
	}
	width := m.detailView.Width
	if width < 20 {
		width = 80
	}
	m.detailView.SetContent(wrapANSI(m.detailRaw, width))
}

// rebuildLogView renders log lines (with optional search highlight + level
// coloring) into the log viewport. Called on data change and on search change.
func (m *Model) rebuildLogView() {
	if len(m.logLines) == 0 {
		if m.logSearch != "" {
			m.logView.SetContent(dimStyle.Render("(검색 결과 없음)"))
		} else {
			m.logView.SetContent(dimStyle.Render("(최근 1시간 로그 없음)"))
		}
		return
	}
	needle := strings.ToLower(m.logSearch)
	width := m.logView.Width
	if width < 20 {
		width = 80
	}
	var b strings.Builder
	shown := 0
	for _, ln := range m.logLines {
		if needle != "" && !strings.Contains(strings.ToLower(ln), needle) {
			continue
		}
		shown++
		style := logLevelStyle(ln)
		for _, seg := range wrapLine(ln, width) {
			if needle != "" {
				b.WriteString(highlightSearch(seg, m.logSearch, style))
			} else {
				b.WriteString(style.Render(seg))
			}
			b.WriteByte('\n')
		}
	}
	if shown == 0 {
		m.logView.SetContent(dimStyle.Render("(검색 결과 없음)"))
		return
	}
	m.logView.SetContent(b.String())
}

// Run starts the bubbletea program.
func Run(version, profile string, cfg config.Config) error {
	m := New(version, profile, cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}