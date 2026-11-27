package zulu

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

type comateAgentUsage struct {
	known      bool
	input      int
	output     int
	cacheRead  int
	cacheWrite int
}

func (s *zuluSession) buildComateAgentArgs() []string {
	args := make([]string, 0, len(s.extraArgs)+16)
	args = append(args, s.extraArgs...)
	args = append(args, "-p", "--mode", "json", "-w", s.workDir)
	if s.username != "" {
		args = append(args, "--username", s.username)
	}
	if cid := s.CurrentSessionID(); cid != "" {
		sessPath := cid
		if !filepath.IsAbs(sessPath) {
			sessPath = filepath.Join(s.workDir, ".comate", "bothouse-sessions", cid+".jsonl")
		}
		args = append(args, "--session", sessPath)
	}
	if m := s.currentModel(); m != "" {
		args = append(args, "--model", m)
	}
	if s.thinking != "" {
		args = append(args, "--thinking", s.thinking)
	} else if s.effort != "" {
		args = append(args, "--thinking", s.effort)
	}
	if s.appendPrompt != "" {
		args = append(args, "--append-system-prompt", s.appendPrompt)
	}
	if s.mode == "yolo" {
		args = append(args, "--permission-mode", "full-access")
	} else {
		args = append(args, "--permission-mode", "approval-required")
	}
	return args
}

func (s *zuluSession) runComateAgentTurn(prompt, messageID string, images []core.ImageAttachment, files []core.FileAttachment) {
	var imgPaths, filePaths []string
	if len(images) > 0 {
		imgPaths = saveImagesToDisk(s.workDir, messageID, images)
	}
	if len(files) > 0 {
		filePaths = core.SaveFilesToDisk(s.workDir, messageID, files)
	}
	if len(imgPaths) > 0 || len(filePaths) > 0 {
		var sb strings.Builder
		sb.WriteString(prompt)
		for _, p := range imgPaths {
			sb.WriteString("\n[Attached image: " + p + "]")
		}
		for _, p := range filePaths {
			sb.WriteString("\n[Attached file: " + p + "]")
		}
		prompt = sb.String()
	}

	args := s.buildComateAgentArgs()
	cmd := exec.CommandContext(s.ctx, s.cmd, args...)
	cmd.Dir = s.workDir
	cmd.Stdin = strings.NewReader(prompt)

	// Provide default fallback context window / max tokens for newer models not yet
	// listed in comate-agent's offline catalog.
	extra := append([]string(nil), s.extraEnv...)
	if !hasEnvKey(extra, "ONEAPI_CONTEXT_WINDOW") {
		extra = append(extra, "ONEAPI_CONTEXT_WINDOW=1000000")
	}
	if !hasEnvKey(extra, "ONEAPI_MAX_TOKENS") {
		extra = append(extra, "ONEAPI_MAX_TOKENS=32000")
	}
	cmd.Env = core.MergeEnv(cmd.Environ(), extra)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.emitError(fmt.Errorf("comate-agent: stdout pipe: %w", err))
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		s.emitError(fmt.Errorf("comate-agent: start %s: %w", s.cmd, err))
		return
	}

	var streamedBytes int
	var fullText strings.Builder
	var lastUsage comateAgentUsage
	var completedEmitted bool

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		done := s.handleComateAgentLine(scanner.Text(), &streamedBytes, &fullText, &lastUsage)
		if done {
			completedEmitted = true
		}
	}

	waitErr := cmd.Wait()
	if !completedEmitted {
		if waitErr != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				s.emitError(fmt.Errorf("comate-agent: %w: %s", waitErr, msg))
			} else {
				s.emitError(fmt.Errorf("comate-agent: %w", waitErr))
			}
			return
		}
		s.emit(core.Event{
			Type:                     core.EventResult,
			Content:                  fullText.String(),
			StreamedContentBytes:     streamedBytes,
			SessionID:                s.CurrentSessionID(),
			UsageKnown:               lastUsage.known,
			InputTokens:              lastUsage.input,
			OutputTokens:             lastUsage.output,
			CacheReadInputTokens:     lastUsage.cacheRead,
			CacheCreationInputTokens: lastUsage.cacheWrite,
			Done:                     true,
		})
	}
}

type comateAgentLine struct {
	Type                  string `json:"type"`
	ID                    string `json:"id"`
	ToolName              string `json:"toolName"`
	FinalText             string `json:"finalText"`
	AssistantMessageEvent *struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	} `json:"assistantMessageEvent"`
	Message *struct {
		Usage *struct {
			Input      int `json:"input"`
			Output     int `json:"output"`
			CacheRead  int `json:"cacheRead"`
			CacheWrite int `json:"cacheWrite"`
		} `json:"usage"`
	} `json:"message"`
	Error string `json:"error"`
}

func (s *zuluSession) handleComateAgentLine(line string, streamedBytes *int, fullText *strings.Builder, lastUsage *comateAgentUsage) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var ev comateAgentLine
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return false
	}

	switch ev.Type {
	case "session":
		if ev.ID != "" && s.CurrentSessionID() == "" {
			s.sessionID.Store(ev.ID)
		}
	case "message_update":
		if ev.AssistantMessageEvent != nil && ev.AssistantMessageEvent.Type == "text_delta" && ev.AssistantMessageEvent.Delta != "" {
			*streamedBytes += len(ev.AssistantMessageEvent.Delta)
			fullText.WriteString(ev.AssistantMessageEvent.Delta)
			s.emit(core.Event{
				Type:    core.EventText,
				Content: ev.AssistantMessageEvent.Delta,
			})
		}
	case "tool_execution_start":
		if ev.ToolName != "" {
			s.emit(core.Event{
				Type:     core.EventToolUse,
				ToolName: ev.ToolName,
			})
		}
	case "turn_end":
		if ev.Message != nil && ev.Message.Usage != nil {
			u := ev.Message.Usage
			*lastUsage = comateAgentUsage{
				known:      true,
				input:      u.Input,
				output:     u.Output,
				cacheRead:  u.CacheRead,
				cacheWrite: u.CacheWrite,
			}
		}
	case "agent_end":
		if ev.Error != "" {
			s.emitError(fmt.Errorf("comate-agent: %s", ev.Error))
			return true
		}
		content := fullText.String()
		if content == "" && ev.FinalText != "" {
			content = ev.FinalText
		}
		if *streamedBytes > len(content) {
			*streamedBytes = len(content)
		}
		s.emit(core.Event{
			Type:                     core.EventResult,
			Content:                  content,
			StreamedContentBytes:     *streamedBytes,
			SessionID:                s.CurrentSessionID(),
			UsageKnown:               lastUsage.known,
			InputTokens:              lastUsage.input,
			OutputTokens:             lastUsage.output,
			CacheReadInputTokens:     lastUsage.cacheRead,
			CacheCreationInputTokens: lastUsage.cacheWrite,
			Done:                     true,
		})
		return true
	}
	return false
}
