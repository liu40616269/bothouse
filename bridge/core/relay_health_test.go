package core

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestClassifyRelayProbeError(t *testing.T) {
	state, _ := classifyRelayProbeError("user@relay.example.com: Permission denied (keyboard-interactive).")
	if state != "auth_expired" {
		t.Fatalf("got %q, want auth_expired", state)
	}
	state, _ = classifyRelayProbeError("ssh: connect to host relay.example.com port 22: Operation timed out")
	if state != "unreachable" {
		t.Fatalf("got %q, want unreachable", state)
	}
}

func TestRelayHealthMonitor_TransitionsAndDedupe(t *testing.T) {
	var mu sync.Mutex
	curState, curMsg := "ok", "ControlMaster 已连接"
	var alerts []string

	m := NewRelayHealthMonitor(
		func() TeamRemoteState {
			return TeamRemoteState{
				Enabled:    true,
				RemoteHost: "dev-box",
				RelayHost:  "relay.example.com",
				Projects:   []string{"cc", "gpt"},
			}
		},
		func(title, body string) {
			mu.Lock()
			alerts = append(alerts, title+": "+body)
			mu.Unlock()
		},
	)
	m.probe = func(_ context.Context, _ TeamRemoteState) (string, string) {
		mu.Lock()
		defer mu.Unlock()
		return curState, curMsg
	}

	// 1. Initial OK: no alert.
	st := m.CheckNow(context.Background())
	if st.State != "ok" {
		t.Fatalf("got %q, want ok", st.State)
	}
	if len(alerts) != 0 {
		t.Fatalf("unexpected initial alert: %v", alerts)
	}

	// 2. Auth expires: sends exactly one alert.
	mu.Lock()
	curState, curMsg = "auth_expired", "SSH 认证已过期"
	mu.Unlock()
	st = m.CheckNow(context.Background())
	if st.State != "auth_expired" {
		t.Fatalf("got %q, want auth_expired", st.State)
	}
	// Repeat check while still expired: must not spam.
	m.CheckNow(context.Background())
	if len(alerts) != 1 || !strings.Contains(alerts[0], "过期") {
		t.Fatalf("expected 1 expiry alert, got %v", alerts)
	}

	// 3. Recovers: sends recovery notice.
	mu.Lock()
	curState, curMsg = "ok", "ControlMaster 已连接"
	mu.Unlock()
	st = m.CheckNow(context.Background())
	if st.State != "ok" {
		t.Fatalf("got %q, want ok", st.State)
	}
	if len(alerts) != 2 || !strings.Contains(alerts[1], "恢复") {
		t.Fatalf("expected recovery alert, got %v", alerts)
	}
}
