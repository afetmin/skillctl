package service

import (
	"context"
	"path/filepath"
	"testing"

	"skillctl/internal/codex"
	"skillctl/internal/config"
	"skillctl/internal/model"
)

func TestManagerListUsesInstalledPluginsAsAuthoritativeInventory(t *testing.T) {
	manual := false
	cwd := "/workspace/current-project"
	client := &fakeCodexSession{
		installed: codex.NewInstalledPlugins(
			codex.InstalledPlugin{ID: "github@openai-curated-remote", Marketplace: "openai-curated-remote", Name: "github", Installed: true, Enabled: true},
			codex.InstalledPlugin{ID: "github@openai-curated", Marketplace: "openai-curated", Name: "github", Installed: false, Enabled: true},
			codex.InstalledPlugin{ID: "disabled-plugin@marketplace", Marketplace: "marketplace", Name: "disabled-plugin", Installed: true, Enabled: false},
		),
		skills: []model.Skill{
			pluginSkill("openai-curated-remote:github", "github", "/cache/remote/github/SKILL.md", true, &manual),
			pluginSkill("openai-curated:github", "github", "/cache/official/github/SKILL.md", true, nil),
			{ID: "codex:user:agents:personal", Name: "personal", Path: "/home/.agents/skills/personal/SKILL.md", Scope: model.ScopeUser, Enabled: false},
		},
	}

	root := t.TempDir()
	manager := Manager{
		ConfigPath: filepath.Join(root, "config.yaml"),
		StatePath:  filepath.Join(root, "state.json"),
		CWD:        cwd,
		openCodex: func(_ context.Context, _ string, openedCWD string) (codexSession, error) {
			client.openedCWDs = append(client.openedCWDs, openedCWD)
			return client, nil
		},
		discoverFilesystem: func(discoveryCWD string) ([]model.Skill, []string, error) {
			client.filesystemCWDs = append(client.filesystemCWDs, discoveryCWD)
			return []model.Skill{
				pluginSkill("openai-curated:github", "github", "/cache/official/github/SKILL.md", true, nil),
				pluginSkill("marketplace:disabled-plugin", "disabled", "/cache/disabled/SKILL.md", true, nil),
				pluginSkill("marketplace:removed-plugin", "removed", "/cache/removed/SKILL.md", true, nil),
				{ID: "codex:system:system", Name: "system", Path: "/home/.codex/skills/.system/system/SKILL.md", Scope: model.ScopeSystem, Enabled: true},
				{ID: "codex:repo:current-project:project", Name: "project", Path: "/workspace/current-project/.agents/skills/project/SKILL.md", Scope: model.ScopeRepo, Enabled: true},
			}, nil, nil
		},
	}
	if err := config.Save(manager.ConfigPath, config.Default()); err != nil {
		t.Fatal(err)
	}

	items, _, err := manager.List(context.Background(), false)
	if err != nil {
		t.Fatalf("Manager.List() error = %v", err)
	}

	byID := make(map[string]SkillStatus, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, id := range []string{
		"codex:plugin:openai-curated-remote:github:github",
		"codex:user:agents:personal",
		"codex:system:system",
		"codex:repo:current-project:project",
	} {
		if _, ok := byID[id]; !ok {
			t.Errorf("Manager.List() missing %s", id)
		}
	}
	for _, id := range []string{
		"codex:plugin:openai-curated:github:github",
		"codex:plugin:marketplace:disabled-plugin:disabled",
		"codex:plugin:marketplace:removed-plugin:removed",
	} {
		if _, ok := byID[id]; ok {
			t.Errorf("Manager.List() included ineligible plugin Skill %s", id)
		}
	}
	if len(items) != 4 {
		t.Fatalf("Manager.List() returned %d Skills, want 4", len(items))
	}
	if got := byID["codex:plugin:openai-curated-remote:github:github"]; got.PluginID != "github@openai-curated-remote" || got.Actual != model.StateManual {
		t.Errorf("remote GitHub status = {plugin_id:%q actual:%q}, want canonical plugin ID and manual", got.PluginID, got.Actual)
	}
	if byID["codex:repo:current-project:project"].Managed {
		t.Error("project Skill managed without --project")
	}
	if len(client.openedCWDs) != 1 || client.openedCWDs[0] != cwd || len(client.installedCWDs) != 1 || client.installedCWDs[0] != cwd || len(client.filesystemCWDs) != 1 || client.filesystemCWDs[0] != cwd {
		t.Errorf("discovery CWDs = opened %v, installed %v, filesystem %v; want %q", client.openedCWDs, client.installedCWDs, client.filesystemCWDs, cwd)
	}

	projectItems, _, err := manager.List(context.Background(), true)
	if err != nil {
		t.Fatalf("Manager.List(project=true) error = %v", err)
	}
	projectByID := make(map[string]SkillStatus, len(projectItems))
	for _, item := range projectItems {
		projectByID[item.ID] = item
	}
	if !projectByID["codex:repo:current-project:project"].Managed {
		t.Error("project Skill not managed with --project")
	}
}

func pluginSkill(source, name, path string, enabled bool, policy *bool) model.Skill {
	return model.Skill{
		ID:      "codex:plugin:" + source + ":" + name,
		Name:    name,
		Path:    path,
		Scope:   model.ScopePlugin,
		Source:  source,
		Enabled: enabled,
		Policy:  policy,
	}
}

type fakeCodexSession struct {
	installed      codex.InstalledPlugins
	skills         []model.Skill
	openedCWDs     []string
	installedCWDs  []string
	filesystemCWDs []string
	skillCWDs      []string
}

func (f *fakeCodexSession) DiscoverSkills(cwd string) ([]model.Skill, []string, error) {
	f.skillCWDs = append(f.skillCWDs, cwd)
	return f.skills, nil, nil
}

func (f *fakeCodexSession) ListInstalledPlugins(cwd string) (codex.InstalledPlugins, []string, error) {
	f.installedCWDs = append(f.installedCWDs, cwd)
	return f.installed, nil, nil
}

func (f *fakeCodexSession) SetEnabled(string, string, bool) error { return nil }
func (f *fakeCodexSession) Close() error                          { return nil }
