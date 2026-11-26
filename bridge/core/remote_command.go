package core

import (
	"fmt"
	"strings"
)

// TeamRemoteState is the engine-facing view of the global [team_remote] config.
type TeamRemoteState struct {
	Enabled    bool
	RemoteHost string
	RelayHost  string
	WorkDir    string
	SSHArgs    []string
	Projects   []string
}

// SetTeamRemoteHooks wires read/write callbacks for the /remote slash command.
func (e *Engine) SetTeamRemoteHooks(get func() TeamRemoteState, save func(TeamRemoteState) error) {
	e.getTeamRemote = get
	e.saveTeamRemote = save
}

func (e *Engine) cmdRemote(p Platform, msg *Message, args []string) {
	if e.getTeamRemote == nil || e.saveTeamRemote == nil {
		e.reply(p, msg.ReplyCtx, "❌ 当前未启用远程开发切换配置，请在 Web 后台 `/system` 中配置团队远程开发机。")
		return
	}

	st := e.getTeamRemote()
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}

	switch sub {
	case "", "status":
		var sb strings.Builder
		if st.Enabled {
			sb.WriteString("🖥️ **团队远程开发机：已开启（远程 Linux）**\n")
		} else {
			sb.WriteString("💻 **团队远程开发机：已关闭（本机运行）**\n")
		}
		if st.RemoteHost != "" {
			sb.WriteString(fmt.Sprintf("- 开发机：`%s`\n", st.RemoteHost))
		}
		if st.RelayHost != "" {
			sb.WriteString(fmt.Sprintf("- 跳板机：`%s`\n", st.RelayHost))
		}
		if st.WorkDir != "" {
			sb.WriteString(fmt.Sprintf("- 远程目录：`%s`\n", st.WorkDir))
		}
		if len(st.Projects) > 0 {
			sb.WriteString(fmt.Sprintf("- 共享角色：`%s`\n", strings.Join(st.Projects, ", ")))
		}
		sb.WriteString("\n可用指令：\n- `/remote on`：切到远程 Linux 开发机\n- `/remote off`（或 `/remote local`）：切回本机运行\n- `/remote dir <远端路径>`：切换远程工作目录并开启远程模式")
		e.reply(p, msg.ReplyCtx, sb.String())
		return

	case "on", "enable", "ssh":
		if strings.TrimSpace(st.RemoteHost) == "" || strings.TrimSpace(st.WorkDir) == "" {
			e.reply(p, msg.ReplyCtx, "❌ 尚未配置远程开发机或远程目录，请先在 Web 后台 `/system` 填好一次，之后即可用 `/remote on` / `/remote off` 一键切换。")
			return
		}
		if st.Enabled {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ 当前已经是远程 Linux 模式（`%s` · `%s`）。", st.RemoteHost, st.WorkDir))
			return
		}
		st.Enabled = true
		if err := e.saveTeamRemote(st); err != nil {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 切换远程模式失败：%v", err))
			return
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("🖥️ 已切换到**远程 Linux 开发机**（`%s` · `%s`），正在重启 Bothouse 使全部角色生效…", st.RemoteHost, st.WorkDir))
		select {
		case RestartCh <- RestartRequest{SessionKey: msg.SessionKey, Platform: p.Name()}:
		default:
		}
		return

	case "off", "disable", "local":
		if !st.Enabled {
			e.reply(p, msg.ReplyCtx, "✅ 当前已经是**本机运行**模式。")
			return
		}
		st.Enabled = false
		if err := e.saveTeamRemote(st); err != nil {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 切回本机模式失败：%v", err))
			return
		}
		e.reply(p, msg.ReplyCtx, "💻 已切回**本机运行**模式（远程主机与目录配置已保留），正在重启 Bothouse 使全部角色生效…")
		select {
		case RestartCh <- RestartRequest{SessionKey: msg.SessionKey, Platform: p.Name()}:
		default:
		}
		return

	case "dir", "cd":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, "用法：`/remote dir <远端 Linux 目录>`")
			return
		}
		newDir := strings.TrimSpace(strings.Join(args[1:], " "))
		if newDir == "" {
			e.reply(p, msg.ReplyCtx, "用法：`/remote dir <远端 Linux 目录>`")
			return
		}
		if strings.TrimSpace(st.RemoteHost) == "" {
			e.reply(p, msg.ReplyCtx, "❌ 尚未配置远程开发机，请先在 Web 后台 `/system` 配置一次。")
			return
		}
		st.WorkDir = newDir
		st.Enabled = true
		if err := e.saveTeamRemote(st); err != nil {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("❌ 更新远程目录失败：%v", err))
			return
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf("🖥️ 已更新远程工作目录为 `%s` 并开启远程模式（`%s`），正在重启 Bothouse 生效…", st.WorkDir, st.RemoteHost))
		select {
		case RestartCh <- RestartRequest{SessionKey: msg.SessionKey, Platform: p.Name()}:
		default:
		}
		return

	default:
		e.reply(p, msg.ReplyCtx, "用法：\n- `/remote`：查看当前状态\n- `/remote on`：切到远程 Linux 开发机\n- `/remote off` / `/remote local`：切回本机运行\n- `/remote dir <远端路径>`：切换远程目录")
	}
}
