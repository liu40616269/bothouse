package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const idevComateAgentCLIPath = "/Applications/iDev.app/Contents/Resources/app/extensions/baiducomate.comate/dist/agent-daemon/comate-agent/dist/comate-agent-cli.js"

// ResolveLocalComateAgent locates the new iDev bundled `comate-agent` CLI (which
// replaced `dist/zulu-cli/bin/zulu` in newer iDev releases). Returns either a
// standalone `comate-agent` binary (scriptPath=="") or `(nodePath, scriptPath, true)`.
func ResolveLocalComateAgent() (execPath string, scriptPath string, ok bool) {
	if bin, err := ResolveCLI("comate-agent"); err == nil && bin != "" {
		return bin, "", true
	}
	candidates := []string{idevComateAgentCLIPath}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, "Applications", "iDev.app", "Contents", "Resources", "app", "extensions", "baiducomate.comate", "dist", "agent-daemon", "comate-agent", "dist", "comate-agent-cli.js"),
		)
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			nodeBin, err := ResolveCLI("node")
			if err != nil {
				continue
			}
			return nodeBin, p, true
		}
	}
	return "", "", false
}

// ParseComateAgentModelsJSON parses `comate-agent --list-models --mode json` output.
func ParseComateAgentModelsJSON(raw []byte) []ModelOption {
	text := strings.TrimSpace(string(raw))
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return nil
	}
	var payload struct {
		Models []struct {
			Model       string `json:"model"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &payload); err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]ModelOption, 0, len(payload.Models))
	for _, m := range payload.Models {
		name := strings.TrimSpace(m.Model)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		desc := strings.TrimSpace(m.DisplayName)
		if desc == name {
			desc = ""
		}
		out = append(out, ModelOption{Name: name, Desc: desc})
	}
	return out
}
