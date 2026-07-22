package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skillctl/internal/codex"
	"skillctl/internal/config"
	"skillctl/internal/model"
	statestore "skillctl/internal/state"
)

func TestManagerListUsesInstalledPluginsAsAuthoritativeInventory(t *testing.T) {
	manual := false
	cwd := "/workspace/current-project"
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	remotePath := filepath.Join(cacheRoot, "openai-curated-remote", "github", "1.0.0", "skills", "github", "SKILL.md")
	client := &fakeCodexSession{
		installed: codex.NewInstalledPlugins(
			codex.InstalledPlugin{ID: "github@openai-curated-remote", Marketplace: "openai-curated-remote", Name: "github", Version: "1.0.0", Installed: true, Enabled: true},
			codex.InstalledPlugin{ID: "github@openai-curated", Marketplace: "openai-curated", Name: "github", Installed: false, Enabled: true},
			codex.InstalledPlugin{ID: "disabled-plugin@marketplace", Marketplace: "marketplace", Name: "disabled-plugin", Installed: true, Enabled: false},
		),
		skills: []model.Skill{
			pluginSkill("openai-curated-remote:github", "github", remotePath, true, &manual),
			pluginSkill("openai-curated:github", "github", "/cache/official/github/SKILL.md", true, nil),
			{ID: "codex:user:agents:personal", Name: "personal", Path: "/home/.agents/skills/personal/SKILL.md", Scope: model.ScopeUser, Enabled: false},
		},
	}

	manager := Manager{
		ConfigPath:      filepath.Join(root, "config.yaml"),
		StatePath:       filepath.Join(root, "state.json"),
		CWD:             cwd,
		pluginCacheRoot: cacheRoot,
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

func TestManagerListSupplementsOnlyExactInstalledPluginVersion(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	currentPath := writePluginSkill(t, cacheRoot, "market", "tools", "2.0.0", "current")
	writePluginSkill(t, cacheRoot, "market", "tools", "1.0.0", "stale")
	localPath := writePluginSkill(t, cacheRoot, "local-market", "bundled", "2026.07", "local")
	client := &fakeCodexSession{installed: codex.NewInstalledPlugins(
		codex.InstalledPlugin{ID: "tools@market", Marketplace: "market", Name: "tools", Version: "2.0.0", Installed: true, Enabled: true},
		codex.InstalledPlugin{ID: "bundled@local-market", Marketplace: "local-market", Name: "bundled", Version: "remote-version", LocalVersion: "2026.07", SourceType: "local", SourcePath: filepath.Join(root, "source"), Installed: true, Enabled: true},
	)}
	manager := testManager(t, root, cacheRoot, client, nil)

	items, discovery, err := manager.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !discovery.Complete() {
		t.Fatalf("discovery status = %q, want complete", discovery.Status)
	}
	if len(items) != 2 {
		t.Fatalf("Manager.List() returned %d skills, want 2", len(items))
	}
	byName := map[string]SkillStatus{}
	for _, item := range items {
		byName[item.Name] = item
	}
	if byName["current"].Path != currentPath || byName["current"].Actual != model.StateDisabled {
		t.Fatalf("remote supplemented skill = %#v", byName["current"])
	}
	if byName["local"].Path != localPath || byName["local"].PluginID != "bundled@local-market" {
		t.Fatalf("local supplemented skill = %#v", byName["local"])
	}
}

func TestManagerListReportsMissingExactPluginPackageWithoutSubstitution(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	writePluginSkill(t, cacheRoot, "market", "tools", "1.0.0", "stale")
	client := &fakeCodexSession{installed: codex.NewInstalledPlugins(codex.InstalledPlugin{
		ID: "tools@market", Marketplace: "market", Name: "tools", Version: "2.0.0", Installed: true, Enabled: true,
	})}
	manager := testManager(t, root, cacheRoot, client, nil)

	items, discovery, err := manager.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("Manager.List() substituted another version: %#v", items)
	}
	if !hasWarning(discovery, "plugin_package_incomplete") {
		t.Fatalf("warnings = %#v, want plugin_package_incomplete", discovery.Warnings)
	}
}

func TestManagerListRefreshesInstalledPluginVersion(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	writePluginSkill(t, cacheRoot, "market", "tools", "1.0.0", "first")
	writePluginSkill(t, cacheRoot, "market", "tools", "2.0.0", "second")
	client := &fakeCodexSession{installedSequence: []codex.InstalledPlugins{
		codex.NewInstalledPlugins(codex.InstalledPlugin{ID: "tools@market", Marketplace: "market", Name: "tools", Version: "1.0.0", Installed: true, Enabled: true}),
		codex.NewInstalledPlugins(codex.InstalledPlugin{ID: "tools@market", Marketplace: "market", Name: "tools", Version: "2.0.0", Installed: true, Enabled: true}),
	}}
	manager := testManager(t, root, cacheRoot, client, nil)

	first, _, err := manager.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := manager.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Name != "first" || len(second) != 1 || second[0].Name != "second" {
		t.Fatalf("refresh results = first %#v, second %#v", first, second)
	}
}

func TestDiscoveryFailureDegradesListAndBlocksSync(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "skill", "agents", "openai.yaml")
	local := model.Skill{ID: "codex:user:agents:local", Name: "local", Path: filepath.Join(root, "skill", "SKILL.md"), PolicyPath: policyPath, Scope: model.ScopeUser, Enabled: true}
	client := &fakeCodexSession{
		installedErr: &codex.RPCError{Method: "plugin/installed", Code: -32601, Message: "method not found"},
		skills:       []model.Skill{local, pluginSkill("market:cached", "cached", filepath.Join(root, "cache", "market", "cached", "1", "SKILL.md"), true, nil)},
	}
	manager := testManager(t, root, filepath.Join(root, "cache"), client, []model.Skill{local})

	items, discovery, err := manager.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Status != model.DiscoveryPartialUnsupported || len(items) != 1 || items[0].ID != local.ID {
		t.Fatalf("partial list = status %q items %#v", discovery.Status, items)
	}
	report, err := manager.Sync(context.Background(), SyncOptions{})
	var unavailable *InventoryUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("Sync() error = %v, want InventoryUnavailableError", err)
	}
	if report.Changed != 0 {
		t.Fatalf("Sync() changed = %d, want 0 before mutation", report.Changed)
	}
	if _, err := os.Stat(policyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("policy path was mutated before discovery completed: %v", err)
	}
}

func TestDiscoveryTransientFailureIsDistinguished(t *testing.T) {
	root := t.TempDir()
	client := &fakeCodexSession{installedErr: errors.New("connection reset")}
	manager := testManager(t, root, filepath.Join(root, "cache"), client, nil)

	_, discovery, err := manager.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Status != model.DiscoveryPartialFailure || !hasWarning(discovery, "plugin_discovery_failed") {
		t.Fatalf("discovery = %#v", discovery)
	}
}

func TestExplicitNonPluginSetContinuesDuringPluginDiscoveryFailure(t *testing.T) {
	root := t.TempDir()
	policyPath := filepath.Join(root, "skill", "agents", "openai.yaml")
	local := model.Skill{ID: "codex:user:agents:local", Name: "local", Path: filepath.Join(root, "skill", "SKILL.md"), PolicyPath: policyPath, Scope: model.ScopeUser, Source: "agents", Enabled: true}
	client := &fakeCodexSession{
		installedErr: &codex.RPCError{Method: "plugin/installed", Code: -32601, Message: "method not found"},
		skills:       []model.Skill{local},
	}
	manager := testManager(t, root, filepath.Join(root, "cache"), client, []model.Skill{local})

	skill, report, err := manager.Set(context.Background(), local.ID, model.StateManual, false, false)
	if err != nil {
		t.Fatalf("Manager.Set() error = %v", err)
	}
	if skill.ID != local.ID || report == nil || report.Changed != 1 || !report.Changes[0].Applied {
		t.Fatalf("Manager.Set() result = skill %#v report %#v", skill, report)
	}
	data, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "allow_implicit_invocation: false") {
		t.Fatalf("policy = %s", data)
	}
}

func TestPluginRestoreRequiresVerifiedInstalledState(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	entry := statestore.Entry{SkillID: "codex:plugin:market:tools:disabled", SkillPath: filepath.Join(root, "cached", "SKILL.md"), PluginID: "tools@market", ManagedEnabled: true}
	state := statestore.Default()
	state.Entries[entry.SkillPath] = entry
	if err := statestore.Save(statePath, state); err != nil {
		t.Fatal(err)
	}
	client := &fakeCodexSession{installedErr: &codex.RPCError{Method: "plugin/installed", Code: -32601, Message: "method not found"}}
	manager := testManager(t, root, filepath.Join(root, "cache"), client, nil)

	_, err := manager.Restore(context.Background(), []string{entry.SkillID}, false, true)
	var unavailable *InventoryUnavailableError
	if !errors.As(err, &unavailable) || unavailable.Status != model.DiscoveryPartialUnsupported {
		t.Fatalf("Restore() error = %v", err)
	}
	if !strings.Contains(err.Error(), "upgrade Codex") {
		t.Fatalf("Restore() error is not actionable: %v", err)
	}
	persisted, err := statestore.LoadOrDefault(statePath)
	if err != nil || len(persisted.Entries) != 1 {
		t.Fatalf("restore state was changed: %#v, %v", persisted, err)
	}
}

func TestDoctorReportsOrphansWithoutReactivatingStaleCache(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	stalePath := writePluginSkill(t, cacheRoot, "market", "tools", "1.0.0", "disabled")
	selector := "codex:plugin:market:tools:disabled"
	cfg := config.Default()
	cfg.Profiles["default"] = config.Profile{Disabled: []string{selector}}
	configPath := filepath.Join(root, "config.yaml")
	statePath := filepath.Join(root, "state.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	state := statestore.Default()
	state.Entries[stalePath] = statestore.Entry{SkillID: selector, SkillPath: stalePath, PluginID: "tools@market"}
	if err := statestore.Save(statePath, state); err != nil {
		t.Fatal(err)
	}
	beforeConfig, _ := os.ReadFile(configPath)
	beforeState, _ := os.ReadFile(statePath)
	client := &fakeCodexSession{installed: codex.NewInstalledPlugins()}
	manager := Manager{
		ConfigPath: configPath, StatePath: statePath, CWD: root, pluginCacheRoot: cacheRoot,
		openCodex: func(context.Context, string, string) (codexSession, error) { return client, nil },
		discoverFilesystem: func(string) ([]model.Skill, []string, error) {
			return []model.Skill{pluginSkill("market:tools", "disabled", stalePath, false, nil)}, nil, nil
		},
	}

	report, err := manager.Sync(context.Background(), SyncOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 0 || len(report.Orphans) != 2 {
		t.Fatalf("doctor report = scanned %d orphans %#v", report.Scanned, report.Orphans)
	}
	afterConfig, _ := os.ReadFile(configPath)
	afterState, _ := os.ReadFile(statePath)
	if string(beforeConfig) != string(afterConfig) || string(beforeState) != string(afterState) {
		t.Fatal("dry-run orphan reporting rewrote retained configuration or restore state")
	}

	client.installed = codex.NewInstalledPlugins(codex.InstalledPlugin{ID: "tools@market", Marketplace: "market", Name: "tools", Version: "1.0.0", Installed: true, Enabled: true})
	client.skills = []model.Skill{pluginSkill("market:tools", "disabled", stalePath, false, nil)}
	reinstalled, err := manager.Sync(context.Background(), SyncOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(reinstalled.Orphans) != 0 {
		t.Fatalf("reinstalled canonical plugin still orphaned: %#v", reinstalled.Orphans)
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

func writePluginSkill(t *testing.T, cacheRoot, marketplace, plugin, version, name string) string {
	t.Helper()
	path := filepath.Join(cacheRoot, marketplace, plugin, version, "skills", name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("---\nname: " + name + "\ndescription: test skill\n---\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testManager(t *testing.T, root, cacheRoot string, client *fakeCodexSession, filesystem []model.Skill) Manager {
	t.Helper()
	manager := Manager{
		ConfigPath:      filepath.Join(root, "config.yaml"),
		StatePath:       filepath.Join(root, "state.json"),
		CWD:             root,
		pluginCacheRoot: cacheRoot,
		openCodex: func(context.Context, string, string) (codexSession, error) {
			return client, nil
		},
		discoverFilesystem: func(string) ([]model.Skill, []string, error) {
			return filesystem, nil, nil
		},
	}
	if err := config.Save(manager.ConfigPath, config.Default()); err != nil {
		t.Fatal(err)
	}
	return manager
}

func hasWarning(discovery model.DiscoveryReport, code string) bool {
	for _, warning := range discovery.Warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

type fakeCodexSession struct {
	installed         codex.InstalledPlugins
	installedSequence []codex.InstalledPlugins
	installedErr      error
	skills            []model.Skill
	skillsErr         error
	openedCWDs        []string
	installedCWDs     []string
	filesystemCWDs    []string
	skillCWDs         []string
}

func (f *fakeCodexSession) DiscoverSkills(cwd string) ([]model.Skill, []string, error) {
	f.skillCWDs = append(f.skillCWDs, cwd)
	return f.skills, nil, f.skillsErr
}

func (f *fakeCodexSession) ListInstalledPlugins(cwd string) (codex.InstalledPlugins, []string, error) {
	f.installedCWDs = append(f.installedCWDs, cwd)
	if f.installedErr != nil {
		return codex.InstalledPlugins{}, nil, f.installedErr
	}
	if len(f.installedSequence) > 0 {
		installed := f.installedSequence[0]
		f.installedSequence = f.installedSequence[1:]
		return installed, nil, nil
	}
	return f.installed, nil, nil
}

func (f *fakeCodexSession) SetEnabled(string, string, bool) error { return nil }
func (f *fakeCodexSession) Close() error                          { return nil }
