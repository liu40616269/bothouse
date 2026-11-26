package zulu

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chenhg5/cc-connect/core"
)

type zuluSession struct {
	cmd          string
	extraArgs    []string
	workDir      string
	remoteHost   string
	relayHost    string
	sshArgs      []string
	username     string
	license      string
	ptoken       string
	model        string
	modelFn      func() string
	mode         string
	workMode     string
	thinking     string
	effort       string
	appendPrompt string
	extraEnv     []string
	attachDir    string

	events    chan core.Event
	sessionID atomic.Value
	ctx       context.Context
	cancel    context.CancelFunc
	sendWg    sync.WaitGroup
	alive     atomic.Bool
}

func newZuluSession(
	cmd string,
	extraArgs []string,
	workDir, remoteHost, relayHost string,
	sshArgs []string,
	username, license, ptoken, model, mode, workMode, thinking, effort, appendPrompt, sessionID string,
	extraEnv []string,
) *zuluSession {
	ctx, cancel := context.WithCancel(context.Background())
	s := &zuluSession{
		cmd:          cmd,
		extraArgs:    extraArgs,
		workDir:      workDir,
		remoteHost:   remoteHost,
		relayHost:    relayHost,
		sshArgs:      sshArgs,
		username:     username,
		license:      license,
		ptoken:       ptoken,
		model:        model,
		mode:         mode,
		workMode:     workMode,
		thinking:     thinking,
		effort:       effort,
		appendPrompt: appendPrompt,
		extraEnv:     extraEnv,
		attachDir:    attachDirForWorkDir(workDir),
		events:       make(chan core.Event, 64),
		ctx:          ctx,
		cancel:       cancel,
	}
	s.sessionID.Store(sessionID)
	s.alive.Store(true)
	return s
}

func (s *zuluSession) Send(prompt string, messageID string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return fmt.Errorf("zulu: session is closed")
	}
	s.sendWg.Add(1)
	go func() {
		defer s.sendWg.Done()
		s.runTurn(prompt, messageID, images, files)
	}()
	return nil
}

func (s *zuluSession) buildArgs(prompt string, imgPaths, filePaths []string) []string {
	args := make([]string, 0, len(s.extraArgs)+24)
	args = append(args, s.extraArgs...)
	args = append(args, "run", "--display", "delta-stream", "--cwd", s.workDir, "-q", prompt)
	if s.username != "" {
		args = append(args, "--username", s.username)
	}
	if s.license != "" {
		args = append(args, "-l", s.license)
	}
	if s.ptoken != "" {
		args = append(args, "-t", s.ptoken)
	}
	if cid := s.CurrentSessionID(); cid != "" {
		args = append(args, "-c", cid)
	}
	if m := s.currentModel(); m != "" {
		args = append(args, "-m", m)
	}
	if s.workMode != "" {
		args = append(args, "--mode", s.workMode)
	}
	if s.thinking != "" {
		args = append(args, "--thinking", s.thinking)
	}
	if s.effort != "" {
		args = append(args, "--effort", s.effort)
	}
	if s.appendPrompt != "" {
		args = append(args, "--append-system-prompt", s.appendPrompt)
	}
	if s.mode == "yolo" {
		args = append(args, "--on-requires-action", "auto-approve")
	}
	for _, p := range imgPaths {
		args = append(args, "--attach-image", p)
	}
	for _, p := range filePaths {
		args = append(args, "--attach-file", p)
	}
	return args
}

func saveImagesToDisk(workDir, messageID string, images []core.ImageAttachment) []string {
	if len(images) == 0 {
		return nil
	}
	asFiles := make([]core.FileAttachment, 0, len(images))
	for i, img := range images {
		name := strings.TrimSpace(img.FileName)
		if name == "" {
			ext := ".png"
			switch strings.ToLower(strings.TrimSpace(img.MimeType)) {
			case "image/jpeg", "image/jpg":
				ext = ".jpg"
			case "image/webp":
				ext = ".webp"
			case "image/gif":
				ext = ".gif"
			}
			name = fmt.Sprintf("image-%d%s", i+1, ext)
		}
		asFiles = append(asFiles, core.FileAttachment{
			MimeType: img.MimeType,
			Data:     img.Data,
			FileName: name,
		})
	}
	return core.SaveFilesToDisk(workDir, messageID, asFiles)
}

func (s *zuluSession) runTurn(prompt, messageID string, images []core.ImageAttachment, files []core.FileAttachment) {
	var imgPaths []string
	var filePaths []string
	if s.remoteHost == "" {
		if len(images) > 0 {
			imgPaths = saveImagesToDisk(s.workDir, messageID, images)
		}
		if len(files) > 0 {
			filePaths = core.SaveFilesToDisk(s.workDir, messageID, files)
		}
	}

	args := s.buildArgs(prompt, imgPaths, filePaths)
	var streamedBytes int
	var completedEmitted bool

	handleLine := func(raw string) {
		line := strings.TrimSpace(raw)
		if line == "" {
			return
		}
		evts, cid, done := parseZuluStreamLine(line, &streamedBytes)
		if cid != "" {
			s.sessionID.Store(cid)
		}
		for _, ev := range evts {
			if ev.Type == core.EventResult || ev.Type == core.EventError {
				completedEmitted = true
			}
			s.emit(ev)
		}
		if done {
			completedEmitted = true
		}
	}

	if s.remoteHost != "" {
		script := core.BuildRemoteZuluScriptWithEnv(s.cmd, args, s.username, s.extraEnv)
		err := core.StreamRemoteScript(s.ctx, core.SSHExecConfig{
			RemoteHost: s.remoteHost,
			RelayHost:  s.relayHost,
			SSHArgs:    s.sshArgs,
		}, script, handleLine)
		if !completedEmitted {
			if err != nil {
				s.emitError(fmt.Errorf("zulu remote (%s): %w", s.remoteHost, err))
				return
			}
			s.emit(core.Event{
				Type:                 core.EventResult,
				StreamedContentBytes: streamedBytes,
				SessionID:            s.CurrentSessionID(),
				Done:                 true,
			})
		}
		return
	}

	cmd := exec.CommandContext(s.ctx, s.cmd, args...)
	cmd.Dir = s.workDir
	if len(s.extraEnv) > 0 {
		cmd.Env = core.MergeEnv(cmd.Environ(), s.extraEnv)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.emitError(fmt.Errorf("zulu: stdout pipe: %w", err))
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		s.emitError(fmt.Errorf("zulu: start %s: %w", s.cmd, err))
		return
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		handleLine(scanner.Text())
	}

	waitErr := cmd.Wait()
	if !completedEmitted {
		if waitErr != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				s.emitError(fmt.Errorf("zulu: %w: %s", waitErr, msg))
			} else {
				s.emitError(fmt.Errorf("zulu: %w", waitErr))
			}
			return
		}
		s.emit(core.Event{
			Type:                 core.EventResult,
			StreamedContentBytes: streamedBytes,
			SessionID:            s.CurrentSessionID(),
			Done:                 true,
		})
	}
}

type zuluStreamLine struct {
	CID     string          `json:"cid"`
	Kind    string          `json:"kind"`
	Status  string          `json:"status"`
	Content string          `json:"content"`
	Error   json.RawMessage `json:"error"`
	Message string          `json:"message"`
	Element *struct {
		Type     string `json:"type"`
		Content  string `json:"content"`
		ToolName string `json:"toolName"`
	} `json:"element"`
	Chunks []struct {
		Kind  string `json:"kind"`
		Delta string `json:"delta"`
	} `json:"chunks"`
}

func zuluErrorText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Message != "" {
			if obj.Code != "" {
				return obj.Code + ": " + strings.TrimSpace(obj.Message)
			}
			return strings.TrimSpace(obj.Message)
		}
		return strings.TrimSpace(obj.Code)
	}
	return trimmed
}

func isZuluThinkingKind(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "thinking", "thought", "reasoning", "thinking-delta", "thought-delta", "reasoning-delta":
		return true
	default:
		return false
	}
}

func parseZuluStreamLine(line string, streamedBytes *int) ([]core.Event, string, bool) {
	var msg zuluStreamLine
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return nil, "", false
	}

	var out []core.Event
	switch msg.Kind {
	case "element-add":
		if msg.Element != nil {
			elemType := strings.ToUpper(strings.TrimSpace(msg.Element.Type))
			switch {
			case isZuluThinkingKind(elemType):
				if msg.Element.Content != "" {
					out = append(out, core.Event{Type: core.EventThinking, Content: msg.Element.Content})
				}
			case elemType == "TEXT":
				if msg.Element.Content != "" {
					*streamedBytes += len(msg.Element.Content)
					out = append(out, core.Event{Type: core.EventText, Content: msg.Element.Content})
				}
			case elemType == "TOOL":
				if msg.Element.ToolName != "" {
					out = append(out, core.Event{Type: core.EventToolUse, ToolName: msg.Element.ToolName})
				}
			}
		}
	case "delta-batch":
		for _, c := range msg.Chunks {
			if c.Delta == "" {
				continue
			}
			chunkKind := strings.ToLower(strings.TrimSpace(c.Kind))
			switch {
			case isZuluThinkingKind(chunkKind):
				out = append(out, core.Event{Type: core.EventThinking, Content: c.Delta})
			case chunkKind == "text-delta":
				*streamedBytes += len(c.Delta)
				out = append(out, core.Event{Type: core.EventText, Content: c.Delta})
			default:
				// Ignore tool-output-delta, tool-params-delta, tool-metadata-patch, etc.
				// so raw command stdout never leaks into the assistant's public reply.
			}
		}
	case "completed":
		if msg.Status != "" && msg.Status != "ok" && msg.Status != "Completed" {
			errText := zuluErrorText(msg.Error)
			if errText == "" {
				errText = msg.Message
			}
			if errText == "" {
				errText = msg.Content
			}
			if errText == "" {
				errText = "zulu run failed with status " + msg.Status
			}
			out = append(out, core.Event{
				Type:      core.EventError,
				Error:     fmt.Errorf("%s", errText),
				SessionID: msg.CID,
				Done:      true,
			})
			return out, msg.CID, true
		}
		// When zulu streamed multiple TEXT segments across tool rounds, completed.content
		// may only hold the final segment (shorter than total streamedBytes). Clamp
		// StreamedContentBytes so core never slices past len(Content).
		streamedForResult := *streamedBytes
		if streamedForResult > len(msg.Content) {
			streamedForResult = len(msg.Content)
		}
		out = append(out, core.Event{
			Type:                 core.EventResult,
			Content:              msg.Content,
			StreamedContentBytes: streamedForResult,
			SessionID:            msg.CID,
			Done:                 true,
		})
		return out, msg.CID, true
	}
	return out, msg.CID, false
}

func (s *zuluSession) emit(ev core.Event) {
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

func (s *zuluSession) emitError(err error) {
	s.emit(core.Event{Type: core.EventError, Error: err, SessionID: s.CurrentSessionID(), Done: true})
}

func (s *zuluSession) RespondPermission(_ string, _ core.PermissionResult) error {
	return nil
}

func (s *zuluSession) Events() <-chan core.Event {
	return s.events
}

func (s *zuluSession) currentModel() string {
	if s.modelFn != nil {
		if m := strings.TrimSpace(s.modelFn()); m != "" {
			return m
		}
	}
	return s.model
}

func (s *zuluSession) CurrentModel() string {
	return s.currentModel()
}

func (s *zuluSession) CurrentSessionID() string {
	v, _ := s.sessionID.Load().(string)
	return v
}

func (s *zuluSession) Alive() bool {
	return s.alive.Load()
}

func (s *zuluSession) Close() error {
	if s.alive.CompareAndSwap(true, false) {
		s.cancel()
		s.sendWg.Wait()
		close(s.events)
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func buildSSHExecArgs(remoteHost string, sshArgs []string, zuluCmd string, zuluArgs []string) []string {
	quotedArgs := make([]string, 0, len(zuluArgs))
	for _, a := range zuluArgs {
		quotedArgs = append(quotedArgs, shellQuote(a))
	}
	joinedArgs := strings.Join(quotedArgs, " ")

	var remoteScript string
	if zuluCmd == "" || zuluCmd == "zulu" {
		// Auto-discover zulu deployed by iDev Remote (~/.comate-server) or ~/.comate/zulu-cli on Linux.
		remoteScript = fmt.Sprintf(
			`ZULU_BIN=$(command -v zulu 2>/dev/null || ls -t "$HOME"/.comate-server/bin/*/extensions/baiducomate.comate/dist/zulu-cli/bin/zulu "$HOME"/.comate/zulu-cli/bin/zulu 2>/dev/null | head -n 1); `+
				`[ -n "$ZULU_BIN" ] || { echo "zulu not found on remote host" >&2; exit 127; }; `+
				`exec "$ZULU_BIN" %s`,
			joinedArgs,
		)
	} else {
		remoteScript = fmt.Sprintf("exec %s %s", shellQuote(zuluCmd), joinedArgs)
	}

	out := make([]string, 0, len(sshArgs)+3)
	out = append(out, sshArgs...)
	out = append(out, "-T", remoteHost, remoteScript)
	return out
}
