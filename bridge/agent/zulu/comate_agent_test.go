package zulu

import (
	"slices"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestBuildComateAgentArgs(t *testing.T) {
	sess := newZuluSession(
		"/opt/homebrew/bin/node",
		[]string{"/Applications/iDev.app/comate-agent-cli.js"},
		"/tmp/workspace",
		"", "", nil,
		"alice", "", "",
		"Opus 5.5", "yolo", "", "", "high", "be concise", "sess-123",
		nil,
	)
	sess.comateAgent = true

	args := sess.buildComateAgentArgs()
	for _, want := range []string{
		"/Applications/iDev.app/comate-agent-cli.js",
		"-p", "--mode", "json",
		"-w", "/tmp/workspace",
		"--username", "alice",
		"--model", "Opus 5.5",
		"--thinking", "high",
		"--permission-mode", "full-access",
		"--append-system-prompt", "be concise",
	} {
		if !slices.Contains(args, want) {
			t.Fatalf("expected arg %q in %v", want, args)
		}
	}
	if !slices.Contains(args, "/tmp/workspace/.comate/bothouse-sessions/sess-123.jsonl") {
		t.Fatalf("expected session path in args, got %v", args)
	}
}

func TestHandleComateAgentStream(t *testing.T) {
	sess := newZuluSession("node", nil, "/tmp", "", "", nil, "alice", "", "", "Opus 5.5", "yolo", "", "", "", "", "", nil)
	sess.comateAgent = true

	lines := []string{
		`· thinking: medium`,
		`{"type":"session","version":3,"id":"sess-abc","cwd":"/tmp"}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"secret thought"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hello "}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"world"}}`,
		`{"type":"turn_end","message":{"usage":{"input":100,"output":5,"cacheRead":20,"cacheWrite":10}}}`,
		`{"type":"agent_end","finalText":"hello world"}`,
	}

	var streamed int
	var full strings.Builder
	var usage comateAgentUsage
	done := false
	for _, l := range lines {
		if sess.handleComateAgentLine(l, &streamed, &full, &usage) {
			done = true
		}
	}
	if !done {
		t.Fatalf("expected agent_end to mark turn complete")
	}
	if sess.CurrentSessionID() != "sess-abc" {
		t.Fatalf("expected session id sess-abc, got %q", sess.CurrentSessionID())
	}

	var texts []string
	var result *core.Event
	for len(sess.events) > 0 {
		ev := <-sess.events
		switch ev.Type {
		case core.EventText:
			texts = append(texts, ev.Content)
		case core.EventResult:
			cp := ev
			result = &cp
		}
	}
	if strings.Join(texts, "") != "hello world" {
		t.Fatalf("expected streamed 'hello world', got %q", strings.Join(texts, ""))
	}
	if result == nil || result.Content != "hello world" || !result.UsageKnown || result.InputTokens != 100 || result.OutputTokens != 5 {
		t.Fatalf("unexpected result event: %+v", result)
	}
}

func TestParseComateAgentModelsJSON(t *testing.T) {
	raw := []byte(`{"source":"builtin-catalog","models":[{"model":"Opus 5.5","displayName":"opus 5.5"},{"model":"DeepSeek-V4.1-Flash","displayName":"DeepSeek-V4.1-Flash"}]}`)
	models := core.ParseComateAgentModelsJSON(raw)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "Opus 5.5" || models[0].Desc != "opus 5.5" {
		t.Fatalf("unexpected first model: %+v", models[0])
	}
	if models[1].Name != "DeepSeek-V4.1-Flash" || models[1].Desc != "" {
		t.Fatalf("unexpected second model: %+v", models[1])
	}
}
