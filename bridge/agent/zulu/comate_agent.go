package zulu

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
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
	if len(images) > 0 {
		s.runComateAgentRPCTurn(prompt, messageID, images, files)
		return
	}
	var filePaths []string
	if len(files) > 0 {
		filePaths = core.SaveFilesToDisk(s.workDir, messageID, files)
	}
	if len(filePaths) > 0 {
		var sb strings.Builder
		sb.WriteString(prompt)
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

func (s *zuluSession) buildComateAgentRPCArgs() []string {
	args := make([]string, 0, len(s.extraArgs)+18)
	args = append(args, s.extraArgs...)
	args = append(args, "--mode", "rpc", "--rpc-extensions", "daemon-v1", "-w", s.workDir)
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

func normalizeImageMIME(img core.ImageAttachment) string {
	mime := strings.ToLower(strings.TrimSpace(img.MimeType))
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mime
	case "image/jpg":
		return "image/jpeg"
	}
	if len(img.Data) > 0 {
		detected := http.DetectContentType(img.Data)
		switch detected {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			return detected
		}
	}
	return "image/png"
}

func (s *zuluSession) runComateAgentRPCTurn(prompt, messageID string, images []core.ImageAttachment, files []core.FileAttachment) {
	if len(files) > 0 {
		filePaths := core.SaveFilesToDisk(s.workDir, messageID, files)
		if len(filePaths) > 0 {
			var sb strings.Builder
			sb.WriteString(prompt)
			for _, p := range filePaths {
				sb.WriteString("\n[Attached file: " + p + "]")
			}
			prompt = sb.String()
		}
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "请查看并分析这张图片。"
	}

	args := s.buildComateAgentRPCArgs()
	cmd := exec.CommandContext(s.ctx, s.cmd, args...)
	cmd.Dir = s.workDir

	extra := append([]string(nil), s.extraEnv...)
	if !hasEnvKey(extra, "ONEAPI_CONTEXT_WINDOW") {
		extra = append(extra, "ONEAPI_CONTEXT_WINDOW=1000000")
	}
	if !hasEnvKey(extra, "ONEAPI_MAX_TOKENS") {
		extra = append(extra, "ONEAPI_MAX_TOKENS=32000")
	}
	if !hasEnvKey(extra, "ONEAPI_IMAGE_TOKEN_UPPER_BOUND") {
		extra = append(extra, "ONEAPI_IMAGE_TOKEN_UPPER_BOUND=4096")
	}
	if m := s.currentModel(); m != "" && !hasEnvKey(extra, "COMATE_AGENT_MODEL_CAPABILITIES") {
		if capJSON, err := json.Marshal(map[string]any{
			"models": []map[string]any{{
				"model":         m,
				"contextWindow": 1000000,
				"maxTokens":     32000,
				"imageInput":    true,
			}},
		}); err == nil {
			extra = append(extra, "COMATE_AGENT_MODEL_CAPABILITIES="+string(capJSON))
		}
	}
	cmd.Env = core.MergeEnv(cmd.Environ(), extra)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.emitError(fmt.Errorf("comate-agent rpc: stdin pipe: %w", err))
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.emitError(fmt.Errorf("comate-agent rpc: stdout pipe: %w", err))
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		s.emitError(fmt.Errorf("comate-agent rpc: start %s: %w", s.cmd, err))
		return
	}

	rpcImages := make([]map[string]string, 0, len(images))
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		rpcImages = append(rpcImages, map[string]string{
			"type":     "image",
			"data":     base64.StdEncoding.EncodeToString(img.Data),
			"mimeType": normalizeImageMIME(img),
		})
	}

	initPayload := map[string]any{
		"id":              "init-1",
		"type":            "daemon_initialize",
		"expectedSession": false,
	}
	if m := s.currentModel(); m != "" {
		initPayload["model"] = m
	}
	promptPayload := map[string]any{
		"id":          "turn-1",
		"type":        "daemon_prompt",
		"executionId": "exec-1",
		"message":     prompt,
		"images":      rpcImages,
	}
	initBytes, _ := json.Marshal(initPayload)
	promptBytes, _ := json.Marshal(promptPayload)
	_, _ = stdin.Write(append(initBytes, '\n'))
	_, _ = stdin.Write(append(promptBytes, '\n'))

	var streamedBytes int
	var fullText strings.Builder
	var lastUsage comateAgentUsage
	var completedEmitted bool

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		done := s.handleComateAgentRPCLine(scanner.Text(), &streamedBytes, &fullText, &lastUsage)
		if done {
			completedEmitted = true
			break
		}
	}
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if !completedEmitted {
		if waitErr != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				s.emitError(fmt.Errorf("comate-agent rpc: %w: %s", waitErr, msg))
			} else {
				s.emitError(fmt.Errorf("comate-agent rpc: %w", waitErr))
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

type comateAgentRPCLine struct {
	Type      string          `json:"type"`
	Success   *bool           `json:"success"`
	Error     string          `json:"error"`
	FinalText string          `json:"finalText"`
	Event     json.RawMessage `json:"event"`
}

func (s *zuluSession) handleComateAgentRPCLine(line string, streamedBytes *int, fullText *strings.Builder, lastUsage *comateAgentUsage) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var ev comateAgentRPCLine
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return false
	}
	switch ev.Type {
	case "response":
		if ev.Success != nil && !*ev.Success {
			msg := ev.Error
			if msg == "" {
				msg = "rpc command failed"
			}
			s.emitError(fmt.Errorf("comate-agent: %s", msg))
			return true
		}
	case "daemon_event":
		if len(ev.Event) > 0 {
			var meta struct {
				Type      string `json:"type"`
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(ev.Event, &meta) == nil && meta.Type == "run_started" && meta.SessionID != "" && s.CurrentSessionID() == "" {
				s.sessionID.Store(meta.SessionID)
			}
			// Reuse the standard JSONL event parser for message_update, tool_execution_start, turn_end.
			// Do not terminate on inner agent_end; wait for outer execution_finished.
			inner := string(ev.Event)
			if !strings.Contains(inner, `"type":"agent_end"`) {
				s.handleComateAgentLine(inner, streamedBytes, fullText, lastUsage)
			}
		}
	case "execution_finished":
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
