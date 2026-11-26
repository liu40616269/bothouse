package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRemoteSkillsOutput(t *testing.T) {
	raw := `some banner line
__SKILLS_JSON__={"dirs":["/home/alice/repo/.comate/skills","/home/alice/.comate/skills"],"skills":[{"name":"deploy-check","dir":"/home/alice/repo/.comate/skills/deploy-check","content":"---\nname: Deploy Check\ndescription: Verify release readiness\n---\nCheck rollout status."},{"name":"deploy-check","dir":"/home/alice/.comate/skills/deploy-check","content":"duplicate should be ignored"},{"name":"code-review","dir":"/home/alice/.comate/skills/code-review","content":"Review the diff carefully."}]}
trailing noise`

	dirs, skills := parseRemoteSkillsOutput(raw)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 dirs, got %d: %v", len(dirs), dirs)
	}
	if dirs[0] != "/home/alice/repo/.comate/skills" {
		t.Fatalf("unexpected first dir: %q", dirs[0])
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 deduplicated skills, got %d", len(skills))
	}
	if skills[0].Name != "deploy-check" || skills[0].DisplayName != "Deploy Check" || skills[0].Description != "Verify release readiness" {
		t.Fatalf("unexpected first skill: %+v", skills[0])
	}
	if skills[0].Source != "/home/alice/repo/.comate/skills/deploy-check" {
		t.Fatalf("unexpected first skill source: %q", skills[0].Source)
	}
	if skills[1].Name != "code-review" || skills[1].Prompt != "Review the diff carefully." {
		t.Fatalf("unexpected second skill: %+v", skills[1])
	}
}

func TestSkillRegistryRemoteFetcher(t *testing.T) {
	localRoot := t.TempDir()
	localSkillDir := filepath.Join(localRoot, "local-only")
	if err := os.MkdirAll(localSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localSkillDir, "SKILL.md"), []byte("---\ndescription: Local skill\n---\nRun locally."), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewSkillRegistry()
	reg.SetDirs([]string{localRoot})

	remoteActive := true
	reg.SetRemoteFetcher(func() ([]string, []*Skill, bool) {
		if !remoteActive {
			return nil, nil, false
		}
		return []string{"/home/alice/.comate/skills"}, []*Skill{
			{Name: "remote-only", Description: "Remote skill", Prompt: "Run remotely.", Source: "/home/alice/.comate/skills/remote-only"},
		}, true
	})

	skills := reg.ListAll()
	if len(skills) != 1 || skills[0].Name != "remote-only" {
		t.Fatalf("expected remote-only skill, got %+v", skills)
	}
	if dirs := reg.Dirs(); len(dirs) != 1 || dirs[0] != "/home/alice/.comate/skills" {
		t.Fatalf("expected remote dirs, got %v", dirs)
	}
	if resolved := reg.Resolve("remote_only"); resolved == nil || resolved.Name != "remote-only" {
		t.Fatalf("expected Resolve to find remote-only, got %+v", resolved)
	}

	// Switching off remote mode falls back to local dirs.
	remoteActive = false
	skills = reg.ListAll()
	if len(skills) != 1 || skills[0].Name != "local-only" {
		t.Fatalf("expected fallback to local-only skill, got %+v", skills)
	}
	if dirs := reg.Dirs(); len(dirs) != 1 || dirs[0] != localRoot {
		t.Fatalf("expected local dirs after fallback, got %v", dirs)
	}
}
