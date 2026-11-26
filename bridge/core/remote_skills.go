package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// RemoteSkillProvider is an optional Agent interface for agents running on a
// remote Linux machine over SSH. When RemoteHost is non-empty, SkillRegistry
// discovers skills on the remote host instead of scanning local directories.
type RemoteSkillProvider interface {
	RemoteSkillConfig() (SSHExecConfig, string)
}

type cachedRemoteSkills struct {
	dirs   []string
	skills []*Skill
	at     time.Time
}

type inflightRemoteSkills struct {
	done   chan struct{}
	dirs   []string
	skills []*Skill
	err    error
}

var (
	remoteSkillCacheMu    sync.Mutex
	remoteSkillCache      = map[string]cachedRemoteSkills{}
	remoteSkillInflight   = map[string]*inflightRemoteSkills{}
)

const remoteSkillCacheTTL = 5 * time.Minute

type remoteSkillPayload struct {
	Dirs   []string `json:"dirs"`
	Skills []struct {
		Name    string `json:"name"`
		Dir     string `json:"dir"`
		Content string `json:"content"`
	} `json:"skills"`
}

// InvalidateRemoteSkillCache clears the process-wide remote skill cache.
func InvalidateRemoteSkillCache() {
	remoteSkillCacheMu.Lock()
	remoteSkillCache = map[string]cachedRemoteSkills{}
	remoteSkillCacheMu.Unlock()
}

// FetchRemoteSkills scans skill directories on a remote Linux host (matching
// iDev zulu-cli's project + personal skill directories) in a single SSH call
// and caches the result across roles sharing the same remote target.
func FetchRemoteSkills(ctx context.Context, cfg SSHExecConfig, workDir string) ([]string, []*Skill, error) {
	host := strings.TrimSpace(cfg.RemoteHost)
	relay := strings.TrimSpace(cfg.RelayHost)
	workDir = strings.TrimSpace(workDir)
	if host == "" {
		return nil, nil, nil
	}

	cacheKey := host + "|" + relay + "|" + workDir
	remoteSkillCacheMu.Lock()
	if cached, ok := remoteSkillCache[cacheKey]; ok && time.Since(cached.at) < remoteSkillCacheTTL {
		dirs := append([]string(nil), cached.dirs...)
		skills := append([]*Skill(nil), cached.skills...)
		remoteSkillCacheMu.Unlock()
		return dirs, skills, nil
	}
	if flight, ok := remoteSkillInflight[cacheKey]; ok {
		remoteSkillCacheMu.Unlock()
		select {
		case <-flight.done:
			return append([]string(nil), flight.dirs...), append([]*Skill(nil), flight.skills...), flight.err
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	flight := &inflightRemoteSkills{done: make(chan struct{})}
	remoteSkillInflight[cacheKey] = flight
	remoteSkillCacheMu.Unlock()

	script := buildRemoteSkillsScript(workDir)
	qctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	stdout, stderr, err := RunRemoteScript(qctx, cfg, script)
	if err != nil {
		outPreview := strings.TrimSpace(string(stdout))
		if len(outPreview) > 400 {
			outPreview = outPreview[len(outPreview)-400:]
		}
		slog.Warn("remote skills: SSH scan failed", "host", host, "error", err, "stderr", strings.TrimSpace(string(stderr)), "output", outPreview)
		flight.err = err
		close(flight.done)
		remoteSkillCacheMu.Lock()
		delete(remoteSkillInflight, cacheKey)
		remoteSkillCacheMu.Unlock()
		return nil, nil, err
	}

	dirs, skills := parseRemoteSkillsOutput(string(stdout))
	flight.dirs = append([]string(nil), dirs...)
	flight.skills = append([]*Skill(nil), skills...)
	close(flight.done)

	remoteSkillCacheMu.Lock()
	delete(remoteSkillInflight, cacheKey)
	remoteSkillCache[cacheKey] = cachedRemoteSkills{
		dirs:   append([]string(nil), dirs...),
		skills: append([]*Skill(nil), skills...),
		at:     time.Now(),
	}
	remoteSkillCacheMu.Unlock()
	return dirs, skills, nil
}

func buildRemoteSkillsScript(workDir string) string {
	quotedWorkDir := ShellQuote(workDir)
	return `PYTHONIOENCODING=utf-8 python3 - ` + quotedWorkDir + ` <<'__BOTHOUSE_SKILLS_PY__'
import json, os, sys

work_dir = sys.argv[1].strip() if len(sys.argv) > 1 else ""
home = os.path.expanduser("~")

candidates = []
if work_dir:
    candidates.extend([
        os.path.join(work_dir, ".comate", "skills"),
        os.path.join(work_dir, "skills"),
        os.path.join(work_dir, ".agents", "skills"),
        os.path.join(work_dir, ".claude", "skills"),
    ])
candidates.extend([
    os.path.join(home, ".comate", "skills"),
    os.path.join(home, ".comate", "skills", ".system"),
    os.path.join(home, ".claude", "skills"),
    os.path.join(home, ".agents", "skills"),
])

dirs = []
skills = []
seen_dirs = set()
seen_skills = set()

for root in candidates:
    norm = os.path.normpath(root)
    norm_b = norm.encode("utf-8")
    if norm in seen_dirs or not os.path.isdir(norm_b):
        continue
    seen_dirs.add(norm)
    dirs.append(norm)
    try:
        entries = sorted(os.listdir(norm_b))
    except OSError:
        continue
    for raw_name in entries:
        name = raw_name.decode("utf-8", "replace")
        if name.startswith("."):
            continue
        key = name.lower()
        if key in seen_skills:
            continue
        skill_dir_b = os.path.join(norm_b, raw_name)
        skill_file_b = os.path.join(skill_dir_b, b"SKILL.md")
        if not os.path.isfile(skill_file_b):
            continue
        try:
            with open(skill_file_b, "r", encoding="utf-8", errors="replace") as f:
                content = f.read()
        except OSError:
            continue
        seen_skills.add(key)
        skills.append({"name": name, "dir": os.path.join(norm, name), "content": content})

print("__SKILLS_JSON__=" + json.dumps({"dirs": dirs, "skills": skills}, ensure_ascii=True))
__BOTHOUSE_SKILLS_PY__
`
}

func parseRemoteSkillsOutput(raw string) ([]string, []*Skill) {
	const marker = "__SKILLS_JSON__="
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		jsonStr := line[idx+len(marker):]
		var payload remoteSkillPayload
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			slog.Warn("remote skills: parse JSON failed", "error", err)
			return nil, nil
		}
		skills := make([]*Skill, 0, len(payload.Skills))
		seen := map[string]bool{}
		for _, item := range payload.Skills {
			key := strings.ToLower(item.Name)
			if item.Name == "" || seen[key] {
				continue
			}
			sk := parseSkillMD(item.Name, item.Content, item.Dir)
			if sk == nil {
				continue
			}
			seen[key] = true
			skills = append(skills, sk)
		}
		return payload.Dirs, skills
	}
	return nil, nil
}
