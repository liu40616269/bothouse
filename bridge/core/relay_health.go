package core

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	relayHealthInterval       = 2 * time.Minute
	relayHealthInitialDelay   = 15 * time.Second
	relayHealthProbeTimeout   = 8 * time.Second
)

// RelayHealthStatus describes the latest SSH/relay connectivity state for team_remote.
type RelayHealthStatus struct {
	Enabled   bool     `json:"enabled"`
	Target    string   `json:"target,omitempty"`
	State     string   `json:"state"` // disabled | unknown | ok | auth_expired | unreachable
	Message   string   `json:"message,omitempty"`
	Projects  []string `json:"projects,omitempty"`
	CheckedAt string   `json:"checked_at,omitempty"`
	Since     string   `json:"since,omitempty"`
}

// RelayProbeFunc probes SSH/relay reachability for a TeamRemoteState and returns (state, message).
type RelayProbeFunc func(ctx context.Context, st TeamRemoteState) (state string, msg string)

// RelayNotifyFunc delivers a proactive alert/recovery message to the user.
type RelayNotifyFunc func(title, body string)

// RelayHealthMonitor periodically probes the configured team_remote SSH relay/host
// and sends proactive notifications on state transitions (expired / unreachable / recovered).
type RelayHealthMonitor struct {
	getState func() TeamRemoteState
	probe    RelayProbeFunc
	notify   RelayNotifyFunc
	interval time.Duration
	initWait time.Duration

	mu        sync.Mutex
	status    RelayHealthStatus
	alerted   bool // true while a failure alert is active (cleared on recovery)
	authSess  *relayAuthSession
	stopCh    chan struct{}
	doneCh    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewRelayHealthMonitor constructs a monitor for team_remote SSH/relay login state.
func NewRelayHealthMonitor(getState func() TeamRemoteState, notify RelayNotifyFunc) *RelayHealthMonitor {
	return &RelayHealthMonitor{
		getState: getState,
		probe:    defaultRelayProbe,
		notify:   notify,
		interval: relayHealthInterval,
		initWait: relayHealthInitialDelay,
		status:   RelayHealthStatus{State: "unknown"},
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start launches the background health-check goroutine.
func (m *RelayHealthMonitor) Start() {
	if m == nil {
		return
	}
	m.startOnce.Do(func() {
		go m.loop()
	})
}

// Stop terminates the background health-check goroutine.
func (m *RelayHealthMonitor) Stop() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
	<-m.doneCh
}

// Status returns the most recently observed RelayHealthStatus.
func (m *RelayHealthMonitor) Status() RelayHealthStatus {
	if m == nil {
		return RelayHealthStatus{State: "disabled"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.status
	st.Projects = append([]string(nil), st.Projects...)
	return st
}

// CheckNow runs an immediate probe, updates status, and fires transition alerts if needed.
func (m *RelayHealthMonitor) CheckNow(ctx context.Context) RelayHealthStatus {
	if m == nil || m.getState == nil {
		return RelayHealthStatus{State: "disabled"}
	}
	tr := m.getState()
	now := time.Now()
	nowStr := now.Format(time.RFC3339)

	target := strings.TrimSpace(tr.RelayHost)
	if target == "" {
		target = strings.TrimSpace(tr.RemoteHost)
	}
	if !tr.Enabled || target == "" {
		m.mu.Lock()
		m.status = RelayHealthStatus{
			Enabled:   false,
			State:     "disabled",
			CheckedAt: nowStr,
		}
		m.alerted = false
		st := m.status
		m.mu.Unlock()
		return st
	}

	probeCtx, cancel := context.WithTimeout(ctx, relayHealthProbeTimeout)
	defer cancel()
	state, msg := m.probe(probeCtx, tr)

	var notifyTitle, notifyBody string
	m.mu.Lock()
	prevState := m.status.State
	since := m.status.Since
	if state != prevState || since == "" {
		since = nowStr
	}
	m.status = RelayHealthStatus{
		Enabled:   true,
		Target:    target,
		State:     state,
		Message:   msg,
		Projects:  append([]string(nil), tr.Projects...),
		CheckedAt: nowStr,
		Since:     since,
	}

	rolesLabel := strings.Join(tr.Projects, " / ")
	if rolesLabel == "" {
		rolesLabel = "远端角色"
	}

	switch state {
	case "auth_expired":
		if !m.alerted {
			m.alerted = true
			notifyTitle = "Bothouse 跳板机登录态已过期"
			notifyBody = fmt.Sprintf("⚠️ 跳板机（%s）SSH 登录态已过期，%s 暂时无法执行远端任务。\n请重新认证跳板机（刷新 Relay 会话），认证完成后会自动恢复并通知你。", target, rolesLabel)
		}
	case "unreachable":
		if !m.alerted {
			m.alerted = true
			notifyTitle = "Bothouse 跳板机连接异常"
			notifyBody = fmt.Sprintf("⚠️ 跳板机（%s）当前无法连接（%s），%s 暂时无法执行远端任务。", target, msg, rolesLabel)
		}
	case "ok":
		if m.alerted {
			m.alerted = false
			notifyTitle = "Bothouse 跳板机已恢复"
			notifyBody = fmt.Sprintf("✅ 跳板机（%s）SSH 登录态已恢复正常，%s 可以正常干活了。", target, rolesLabel)
		}
	}
	out := m.status
	out.Projects = append([]string(nil), out.Projects...)
	m.mu.Unlock()

	if state != prevState {
		if state == "ok" {
			slog.Info("relay health: ok", "target", target)
		} else {
			slog.Warn("relay health: degraded", "target", target, "state", state, "message", msg)
		}
	}
	if notifyBody != "" && m.notify != nil {
		m.notify(notifyTitle, notifyBody)
	}
	return out
}

func (m *RelayHealthMonitor) loop() {
	defer close(m.doneCh)

	if m.initWait > 0 {
		timer := time.NewTimer(m.initWait)
		select {
		case <-m.stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	m.CheckNow(context.Background())

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.CheckNow(context.Background())
		}
	}
}

// defaultRelayProbe checks whether the SSH ControlMaster or non-interactive SSH
// session for RelayHost (or RemoteHost) is alive.
func defaultRelayProbe(ctx context.Context, st TeamRemoteState) (string, string) {
	relayHost := strings.TrimSpace(st.RelayHost)
	remoteHost := strings.TrimSpace(st.RemoteHost)
	target := relayHost
	if target == "" {
		target = remoteHost
	}
	if target == "" {
		return "disabled", ""
	}

	// Fast path: check existing ControlMaster socket (<5ms, zero server load).
	checkArgs := make([]string, 0, len(st.SSHArgs)+4)
	checkArgs = append(checkArgs, "-O", "check")
	checkArgs = append(checkArgs, st.SSHArgs...)
	checkArgs = append(checkArgs, target)
	if err := exec.CommandContext(ctx, "ssh", checkArgs...).Run(); err == nil {
		return "ok", "ControlMaster 已连接"
	}

	// Fallback: non-interactive SSH probe to classify expired auth vs network error.
	probeArgs := make([]string, 0, len(st.SSHArgs)+10)
	probeArgs = append(probeArgs,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "LogLevel=ERROR",
		"-o", "StrictHostKeyChecking=no",
	)
	probeArgs = append(probeArgs, st.SSHArgs...)
	if relayHost != "" {
		// For interactive relay bastions, test auth without requesting a remote shell command.
		probeArgs = append(probeArgs, "-N", "-f", target)
	} else {
		probeArgs = append(probeArgs, target, "true")
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ssh", probeArgs...)
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return "ok", "SSH 连接正常"
	}

	errText := strings.TrimSpace(stderr.String())
	if errText == "" {
		errText = err.Error()
	}
	return classifyRelayProbeError(errText)
}

func classifyRelayProbeError(errText string) (string, string) {
	lower := strings.ToLower(errText)
	if strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "keyboard-interactive") ||
		strings.Contains(lower, "authentication failed") ||
		strings.Contains(lower, "no more authentication methods") {
		return "auth_expired", "SSH 认证已过期（需要重新认证跳板机）"
	}
	if strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "could not resolve") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "network is unreachable") ||
		strings.Contains(lower, "no route to host") ||
		strings.Contains(lower, "deadline exceeded") {
		return "unreachable", "网络不可达或连接超时"
	}
	// Strip noisy multi-line SSH banners; keep the last non-empty line.
	lines := strings.Split(errText, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			if len(s) > 120 {
				s = s[:120]
			}
			return "unreachable", s
		}
	}
	return "unreachable", "SSH 检测失败"
}

// SendRelayAlertToEngines sends a proactive alert/recovery notice via Discord
// (preferring owner DM, falling back to an active chat session) plus a macOS
// desktop notification when running on Darwin.
func SendRelayAlertToEngines(engines []*Engine, title, body string) {
	if runtime.GOOS == "darwin" {
		sendDarwinNotification(title, body)
	}
	for _, e := range engines {
		if e == nil {
			continue
		}
		// Prefer owner DM first.
		if dmKey, _, err := e.OpenDMSessionFor("", ""); err == nil && dmKey != "" {
			if err := e.SendToSession(dmKey, body); err == nil {
				slog.Info("relay health: alert sent via owner DM", "project", e.name, "session", dmKey)
				return
			}
		}
	}
	// Fallback: send to the most recent active chat session on any engine.
	for _, e := range engines {
		if e == nil || e.sessions == nil {
			continue
		}
		if sk, _, _ := e.sessions.latestActiveSession(); sk != "" {
			if err := e.SendToSession(sk, body); err == nil {
				slog.Info("relay health: alert sent via active channel", "project", e.name, "session", sk)
				return
			}
		}
	}
	slog.Warn("relay health: could not deliver chat alert (no ready DM or active channel)")
}

func sendDarwinNotification(title, body string) {
	short := strings.ReplaceAll(body, "\n", " ")
	script := fmt.Sprintf("display notification %q with title %q", short, title)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

// SetRelayHealthMonitor attaches the SSH/relay health monitor to the management server.
func (m *ManagementServer) SetRelayHealthMonitor(mon *RelayHealthMonitor) {
	m.mu.Lock()
	m.relayHealth = mon
	m.mu.Unlock()
}

func (m *ManagementServer) handleRelayHealthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	m.mu.RLock()
	mon := m.relayHealth
	m.mu.RUnlock()
	if mon == nil {
		mgmtJSON(w, http.StatusOK, RelayHealthStatus{State: "disabled"})
		return
	}
	mgmtJSON(w, http.StatusOK, mon.Status())
}

func (m *ManagementServer) handleRelayHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	m.mu.RLock()
	mon := m.relayHealth
	m.mu.RUnlock()
	if mon == nil {
		mgmtJSON(w, http.StatusOK, RelayHealthStatus{State: "disabled"})
		return
	}
	mgmtJSON(w, http.StatusOK, mon.CheckNow(r.Context()))
}
