package core

import (
	"context"
	"strings"
	"testing"
)

type captureReplyPlatform struct {
	stubPlatform
	replies []string
}

func (p *captureReplyPlatform) Reply(_ context.Context, _ any, content string) error {
	p.replies = append(p.replies, content)
	return nil
}

func TestCmdRemoteToggleAndDir(t *testing.T) {
	e := NewEngine("cc", &stubAgent{}, nil, "", LangChinese)
	t.Cleanup(func() { e.Stop() })

	state := TeamRemoteState{
		Enabled:    true,
		RemoteHost: "alice@dev-box",
		RelayHost:  "alice@relay.example.internal",
		WorkDir:    "/home/alice/repo-a",
		Projects:   []string{"gpt", "cc", "gemini"},
	}
	e.SetTeamRemoteHooks(
		func() TeamRemoteState { return state },
		func(next TeamRemoteState) error {
			state = next
			return nil
		},
	)

	// Drain any pending RestartCh notifications.
	drainRestart := func() bool {
		select {
		case <-RestartCh:
			return true
		default:
			return false
		}
	}
	for drainRestart() {
	}

	p := &captureReplyPlatform{stubPlatform: stubPlatform{n: "discord"}}
	msg := &Message{SessionKey: "discord:1", ReplyCtx: "r1"}

	// 1. status
	e.cmdRemote(p, msg, nil)
	if len(p.replies) != 1 || !strings.Contains(p.replies[0], "已开启") || !strings.Contains(p.replies[0], "/home/alice/repo-a") {
		t.Fatalf("unexpected status reply: %v", p.replies)
	}

	// 2. off -> disables team_remote, keeps host/workdir, triggers restart
	e.cmdRemote(p, msg, []string{"off"})
	if state.Enabled || state.RemoteHost != "alice@dev-box" || state.WorkDir != "/home/alice/repo-a" {
		t.Fatalf("unexpected state after off: %+v", state)
	}
	if !drainRestart() {
		t.Fatalf("expected RestartCh signal after /remote off")
	}

	// 3. on -> re-enables team_remote, triggers restart
	e.cmdRemote(p, msg, []string{"on"})
	if !state.Enabled {
		t.Fatalf("expected state.Enabled=true after /remote on")
	}
	if !drainRestart() {
		t.Fatalf("expected RestartCh signal after /remote on")
	}

	// 4. dir -> updates remote work_dir, keeps enabled, triggers restart
	e.cmdRemote(p, msg, []string{"dir", "/home/alice/repo-b"})
	if !state.Enabled || state.WorkDir != "/home/alice/repo-b" {
		t.Fatalf("unexpected state after dir: %+v", state)
	}
	if !drainRestart() {
		t.Fatalf("expected RestartCh signal after /remote dir")
	}
}
