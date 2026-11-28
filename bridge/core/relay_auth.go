package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

const (
	relayAuthTimeout     = 2 * time.Minute
	relayAuthMaxOutput   = 16 * 1024
	relayAuthCheckPeriod = 1 * time.Second
)

// RelayAuthSnapshot describes the live state of an interactive SSH/relay auth session.
type RelayAuthSnapshot struct {
	Active    bool     `json:"active"`
	State     string   `json:"state"` // idle | running | succeeded | failed | canceled
	Target    string   `json:"target,omitempty"`
	Output    string   `json:"output,omitempty"`
	URLs      []string `json:"urls,omitempty"`
	Error     string   `json:"error,omitempty"`
	StartedAt string   `json:"started_at,omitempty"`
}

type relayAuthSession struct {
	mu        sync.Mutex
	state     string
	target    string
	output    strings.Builder
	errMsg    string
	startedAt time.Time
	ptmx      *os.File
	cmd       *exec.Cmd
	cancel    context.CancelFunc
}

var (
	ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][0-9A-Za-z]`)
	authURLRe    = regexp.MustCompile(`https?://[^\s"'<>\]\)]+`)
)

// StartAuth launches a PTY-backed interactive SSH ControlMaster session for the
// configured team_remote relay/host so the user can complete keyboard-interactive,
// OTP, push, or browser/Touch-ID auth from the web UI.
func (m *RelayHealthMonitor) StartAuth() (RelayAuthSnapshot, error) {
	if m == nil || m.getState == nil {
		return RelayAuthSnapshot{State: "failed", Error: "relay monitor not configured"}, errors.New("relay monitor not configured")
	}
	tr := m.getState()
	target := strings.TrimSpace(tr.RelayHost)
	if target == "" {
		target = strings.TrimSpace(tr.RemoteHost)
	}
	if !tr.Enabled || target == "" {
		return RelayAuthSnapshot{State: "failed", Error: "team_remote 未启用或未配置跳板机地址"}, errors.New("team_remote not configured")
	}

	// Cancel any existing auth session before starting a fresh one.
	m.CancelAuth()

	// If ControlMaster is already alive, no auth needed.
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer checkCancel()
	if checkControlMaster(checkCtx, tr.SSHArgs, target) {
		m.CheckNow(context.Background())
		return RelayAuthSnapshot{
			Active:    false,
			State:     "succeeded",
			Target:    target,
			Output:    "跳板机 ControlMaster 已处于连接状态，无需重新认证。",
			StartedAt: time.Now().Format(time.RFC3339),
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), relayAuthTimeout)
	args := make([]string, 0, len(tr.SSHArgs)+10)
	args = append(args,
		"-tt",
		"-o", "ControlMaster=yes",
		"-o", "ControlPersist=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
	)
	args = append(args, tr.SSHArgs...)
	args = append(args, target)

	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LC_ALL=en_US.UTF-8")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return RelayAuthSnapshot{State: "failed", Target: target, Error: err.Error()}, err
	}

	sess := &relayAuthSession{
		state:     "running",
		target:    target,
		startedAt: time.Now(),
		ptmx:      ptmx,
		cmd:       cmd,
		cancel:    cancel,
	}

	m.mu.Lock()
	m.authSess = sess
	m.mu.Unlock()

	slog.Info("relay auth: started interactive SSH session", "target", target)

	// Reader goroutine: stream PTY output into session buffer.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				sess.appendOutput(string(buf[:n]))
			}
			if rerr != nil {
				if !errors.Is(rerr, io.EOF) {
					// Expected when ptmx closes on completion/cancel.
				}
				return
			}
		}
	}()

	// Watcher goroutine: wait for either ControlMaster becoming live or cmd exit.
	go func() {
		waitDone := make(chan error, 1)
		go func() { waitDone <- cmd.Wait() }()

		ticker := time.NewTicker(relayAuthCheckPeriod)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
				ok := checkControlMaster(cctx, tr.SSHArgs, target)
				ccancel()
				if ok {
					sess.finish("succeeded", "")
					// ControlPersist=yes keeps the master socket alive after closing the foreground client.
					_ = ptmx.Close()
					cancel()
					slog.Info("relay auth: ControlMaster established", "target", target)
					m.CheckNow(context.Background())
					return
				}
			case werr := <-waitDone:
				_ = ptmx.Close()
				cancel()
				cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
				ok := checkControlMaster(cctx, tr.SSHArgs, target)
				ccancel()
				if ok {
					sess.finish("succeeded", "")
					slog.Info("relay auth: ControlMaster established on exit", "target", target)
					m.CheckNow(context.Background())
					return
				}
				errText := "认证未完成或已退出"
				if ctx.Err() == context.DeadlineExceeded {
					errText = "认证超时（2 分钟），请重试"
				} else if werr != nil {
					errText = werr.Error()
				}
				sess.finish("failed", errText)
				slog.Warn("relay auth: session ended without ControlMaster", "target", target, "error", errText)
				return
			}
		}
	}()

	return sess.snapshot(), nil
}

// AuthStatus returns the current snapshot of the interactive relay auth session.
func (m *RelayHealthMonitor) AuthStatus() RelayAuthSnapshot {
	if m == nil {
		return RelayAuthSnapshot{State: "idle"}
	}
	m.mu.Lock()
	sess := m.authSess
	m.mu.Unlock()
	if sess == nil {
		return RelayAuthSnapshot{State: "idle"}
	}
	return sess.snapshot()
}

// SendAuthInput writes one line of user input (e.g. OTP / password / confirmation)
// to the running PTY auth session. Input is never logged or persisted.
func (m *RelayHealthMonitor) SendAuthInput(input string) (RelayAuthSnapshot, error) {
	if m == nil {
		return RelayAuthSnapshot{State: "idle"}, errors.New("relay monitor not configured")
	}
	m.mu.Lock()
	sess := m.authSess
	m.mu.Unlock()
	if sess == nil {
		return RelayAuthSnapshot{State: "idle"}, errors.New("no active auth session")
	}
	sess.mu.Lock()
	if sess.state != "running" || sess.ptmx == nil {
		snap := sess.snapshotLocked()
		sess.mu.Unlock()
		return snap, errors.New("auth session is not running")
	}
	ptmx := sess.ptmx
	sess.mu.Unlock()

	if _, err := io.WriteString(ptmx, input+"\n"); err != nil {
		return sess.snapshot(), err
	}
	return sess.snapshot(), nil
}

// CancelAuth terminates any active interactive relay auth session.
func (m *RelayHealthMonitor) CancelAuth() RelayAuthSnapshot {
	if m == nil {
		return RelayAuthSnapshot{State: "idle"}
	}
	m.mu.Lock()
	sess := m.authSess
	m.mu.Unlock()
	if sess == nil {
		return RelayAuthSnapshot{State: "idle"}
	}
	sess.mu.Lock()
	if sess.state == "running" {
		sess.state = "canceled"
		if sess.ptmx != nil {
			_ = sess.ptmx.Close()
		}
		if sess.cancel != nil {
			sess.cancel()
		}
	}
	snap := sess.snapshotLocked()
	sess.mu.Unlock()
	return snap
}

func (s *relayAuthSession) appendOutput(chunk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.output.Len()+len(chunk) > relayAuthMaxOutput {
		cur := s.output.String() + chunk
		if len(cur) > relayAuthMaxOutput {
			cur = cur[len(cur)-relayAuthMaxOutput:]
		}
		s.output.Reset()
		s.output.WriteString(cur)
		return
	}
	s.output.WriteString(chunk)
}

func (s *relayAuthSession) finish(state, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != "running" {
		return
	}
	s.state = state
	s.errMsg = errMsg
}

func (s *relayAuthSession) snapshot() RelayAuthSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *relayAuthSession) snapshotLocked() RelayAuthSnapshot {
	clean := cleanRelayAuthOutput(s.output.String())
	urls := extractRelayAuthURLs(clean)
	return RelayAuthSnapshot{
		Active:    s.state == "running",
		State:     s.state,
		Target:    s.target,
		Output:    clean,
		URLs:      urls,
		Error:     s.errMsg,
		StartedAt: s.startedAt.Format(time.RFC3339),
	}
}

func checkControlMaster(ctx context.Context, sshArgs []string, target string) bool {
	args := make([]string, 0, len(sshArgs)+4)
	args = append(args, "-O", "check")
	args = append(args, sshArgs...)
	args = append(args, target)
	return exec.CommandContext(ctx, "ssh", args...).Run() == nil
}

// cleanRelayAuthOutput strips ANSI sequences, normalizes line endings, and decodes
// OpenSSH's octal-escaped UTF-8 sequences (\345\246\202 -> 如) for readable display.
func cleanRelayAuthOutput(raw string) string {
	s := ansiEscapeRe.ReplaceAllString(raw, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = decodeOctalUTF8(s)
	return strings.TrimSpace(s)
}

func decodeOctalUTF8(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+3 < len(s) &&
			s[i+1] >= '0' && s[i+1] <= '3' &&
			s[i+2] >= '0' && s[i+2] <= '7' &&
			s[i+3] >= '0' && s[i+3] <= '7' {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				out = append(out, byte(v))
				i += 4
				continue
			}
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}

func extractRelayAuthURLs(text string) []string {
	matches := authURLRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	var out []string
	for _, u := range matches {
		u = strings.TrimRight(u, ".,;:!?）)」】")
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

func (m *ManagementServer) handleRelayAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	m.mu.RLock()
	mon := m.relayHealth
	m.mu.RUnlock()
	if mon == nil {
		mgmtError(w, http.StatusBadRequest, "relay monitor not enabled")
		return
	}
	snap, err := mon.StartAuth()
	if err != nil {
		mgmtError(w, http.StatusBadRequest, err.Error())
		return
	}
	mgmtJSON(w, http.StatusOK, snap)
}

func (m *ManagementServer) handleRelayAuthPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	m.mu.RLock()
	mon := m.relayHealth
	m.mu.RUnlock()
	if mon == nil {
		mgmtJSON(w, http.StatusOK, RelayAuthSnapshot{State: "idle"})
		return
	}
	mgmtJSON(w, http.StatusOK, mon.AuthStatus())
}

func (m *ManagementServer) handleRelayAuthInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	m.mu.RLock()
	mon := m.relayHealth
	m.mu.RUnlock()
	if mon == nil {
		mgmtError(w, http.StatusBadRequest, "relay monitor not enabled")
		return
	}
	var body struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	snap, err := mon.SendAuthInput(body.Input)
	if err != nil {
		mgmtError(w, http.StatusBadRequest, err.Error())
		return
	}
	mgmtJSON(w, http.StatusOK, snap)
}

func (m *ManagementServer) handleRelayAuthCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	m.mu.RLock()
	mon := m.relayHealth
	m.mu.RUnlock()
	if mon == nil {
		mgmtJSON(w, http.StatusOK, RelayAuthSnapshot{State: "idle"})
		return
	}
	mgmtJSON(w, http.StatusOK, mon.CancelAuth())
}
