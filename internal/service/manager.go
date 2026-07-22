package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"skillctl/internal/codex"
	"skillctl/internal/config"
	"skillctl/internal/fileutil"
	"skillctl/internal/model"
	"skillctl/internal/policy"
	statestore "skillctl/internal/state"
)

type Manager struct {
	ConfigPath string
	StatePath  string
	CWD        string

	openCodex          codexOpener
	discoverFilesystem filesystemDiscoverer
	pluginCacheRoot    string
}

type codexSession interface {
	DiscoverSkills(cwd string) ([]model.Skill, []string, error)
	ListInstalledPlugins(cwd string) (codex.InstalledPlugins, []string, error)
	SetEnabled(path, name string, enabled bool) error
	Close() error
}

type codexOpener func(context.Context, string, string) (codexSession, error)
type filesystemDiscoverer func(string) ([]model.Skill, []string, error)

type SyncOptions struct {
	DryRun                bool
	Project               bool
	Selectors             []string
	allowPartialNonPlugin bool
}

type InventoryUnavailableError struct {
	Status model.DiscoveryStatus
	Reason string
}

const pluginDiscoveryUpgradeMessage = "this Codex version does not support plugin/installed; upgrade Codex to manage plugin Skills"

func (e *InventoryUnavailableError) Error() string {
	return fmt.Sprintf("authoritative plugin inventory is unavailable (%s): %s", e.Status, e.Reason)
}

type SkillStatus struct {
	model.Skill
	Actual  model.InvocationState `json:"actual"`
	Desired model.InvocationState `json:"desired"`
	Managed bool                  `json:"managed"`
	Journal *statestore.Entry     `json:"-"`
}

type RestoreReport struct {
	Restored  []string `json:"restored"`
	Skipped   []string `json:"skipped,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	DryRun    bool     `json:"dry_run"`
}

func (m Manager) Init(ctx context.Context, force, apply bool) (config.Config, *model.SyncReport, error) {
	if _, err := os.Stat(m.ConfigPath); err == nil && !force {
		return config.Config{}, nil, fmt.Errorf("config already exists at %s (use --force to replace it)", m.ConfigPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, nil, err
	}

	cfg := config.Default()
	if err := config.Save(m.ConfigPath, cfg); err != nil {
		return config.Config{}, nil, err
	}
	if !apply {
		return cfg, nil, nil
	}
	report, err := m.Sync(ctx, SyncOptions{})
	return cfg, &report, err
}

func (m Manager) Discover(ctx context.Context) ([]model.Skill, model.DiscoveryReport, error) {
	cfg, _, err := config.LoadOrDefault(m.ConfigPath)
	if err != nil {
		return nil, model.DiscoveryReport{}, err
	}
	skills, discovery, client, err := m.discover(ctx, cfg)
	if client != nil {
		defer client.Close()
	}
	return skills, discovery, err
}

func (m Manager) List(ctx context.Context, project bool) ([]SkillStatus, model.DiscoveryReport, error) {
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		return nil, model.DiscoveryReport{}, configHint(m.ConfigPath, err)
	}
	skills, discovery, client, err := m.discover(ctx, cfg)
	if client != nil {
		defer client.Close()
	}
	if err != nil {
		return nil, discovery, err
	}
	resolver := newResolver(skills)
	profileWarnings, profileErr := resolver.validateProfile(cfg.Active())
	warningCode := "orphaned_policy"
	if !discovery.Complete() {
		warningCode = "policy_unresolved"
	}
	for _, warning := range profileWarnings {
		discovery.Warnings = append(discovery.Warnings, model.DiscoveryWarning{Code: warningCode, Message: warning})
	}
	if profileErr != nil {
		return nil, discovery, profileErr
	}
	localState, err := statestore.LoadOrDefault(m.StatePath)
	if err != nil {
		return nil, discovery, err
	}
	var result []SkillStatus
	for _, skill := range skills {
		managed := skill.ManagedByDefault() || (project && skill.Scope == model.ScopeRepo)
		actual := skill.ActualState()
		desired := actual
		if managed {
			desired = resolver.desired(cfg, skill)
		}
		status := SkillStatus{
			Skill:   skill,
			Actual:  actual,
			Desired: desired,
			Managed: managed,
		}
		if entry, ok := localState.Entries[skill.Path]; ok {
			entryCopy := entry
			status.Journal = &entryCopy
		}
		result = append(result, status)
	}
	return result, discovery, nil
}

func (m Manager) Resolve(ctx context.Context, selector string) (model.Skill, error) {
	skills, _, err := m.Discover(ctx)
	if err != nil {
		return model.Skill{}, err
	}
	return newResolver(skills).resolve(selector)
}

func (m Manager) Set(ctx context.Context, selector string, desired model.InvocationState, noSync, project bool) (model.Skill, *model.SyncReport, error) {
	skills, report, err := m.SetMany(ctx, map[string]model.InvocationState{selector: desired}, noSync, project)
	if len(skills) == 0 {
		return model.Skill{}, report, err
	}
	return skills[0], report, err
}

func (m Manager) SetMany(ctx context.Context, changes map[string]model.InvocationState, noSync, project bool) ([]model.Skill, *model.SyncReport, error) {
	if len(changes) == 0 {
		return nil, nil, errors.New("no skill changes were provided")
	}
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		return nil, nil, configHint(m.ConfigPath, err)
	}
	discovered, _, client, err := m.discover(ctx, cfg)
	if client != nil {
		_ = client.Close()
	}
	if err != nil {
		return nil, nil, err
	}
	resolver := newResolver(discovered)
	resolved := make([]model.Skill, 0, len(changes))
	desiredByID := map[string]model.InvocationState{}
	for selector, desired := range changes {
		if !desired.Valid() {
			return nil, nil, fmt.Errorf("invalid invocation state %q", desired)
		}
		skill, resolveErr := resolver.resolve(selector)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		if skill.Scope == model.ScopeSystem || skill.Scope == model.ScopeAdmin {
			return nil, nil, fmt.Errorf("%s is a %s skill and is outside skillctl management", skill.ID, skill.Scope)
		}
		if skill.Scope == model.ScopeRepo && !project {
			return nil, nil, errors.New("project skills require --project")
		}
		desiredByID[skill.ID] = desired
		resolved = append(resolved, skill)
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].ID < resolved[j].ID })
	for _, skill := range resolved {
		aliases := append([]string{skill.ConfigName}, skill.Aliases...)
		cfg.SetState(skill.ID, skill.Name, desiredByID[skill.ID], aliases...)
	}
	if err := config.Save(m.ConfigPath, cfg); err != nil {
		return nil, nil, err
	}
	if noSync {
		return resolved, nil, nil
	}
	selectors := make([]string, 0, len(resolved))
	for _, skill := range resolved {
		selectors = append(selectors, skill.ID)
	}
	report, err := m.Sync(ctx, SyncOptions{Project: project, Selectors: selectors, allowPartialNonPlugin: true})
	return resolved, &report, err
}

func (m Manager) Sync(ctx context.Context, options SyncOptions) (model.SyncReport, error) {
	report := model.SyncReport{DryRun: options.DryRun, At: time.Now(), Changes: []model.Change{}}
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		return report, configHint(m.ConfigPath, err)
	}
	localState, err := statestore.LoadOrDefault(m.StatePath)
	if err != nil {
		return report, err
	}
	skills, discovery, client, err := m.discover(ctx, cfg)
	if client != nil {
		defer client.Close()
	}
	report.Discovery = discovery
	report.Warnings = append(report.Warnings, discovery.Warnings...)
	if err != nil {
		return report, err
	}
	if !discovery.Complete() && !partialSyncAllowed(options, skills) {
		return report, inventoryUnavailable(discovery)
	}
	report.Scanned = len(skills)
	resolver := newResolver(skills)
	if discovery.Complete() {
		report.Orphans = orphanRecords(cfg.Active(), localState, resolver)
	}
	profileWarnings, profileErr := resolver.validateProfile(cfg.Active())
	warningCode := "orphaned_policy"
	if !discovery.Complete() {
		warningCode = "policy_unresolved"
	}
	for _, warning := range profileWarnings {
		item := model.DiscoveryWarning{Code: warningCode, Message: warning}
		report.Discovery.Warnings = append(report.Discovery.Warnings, item)
		report.Warnings = append(report.Warnings, item)
	}
	if profileErr != nil {
		return report, profileErr
	}
	selected := selectionSet(options.Selectors)

	for _, skill := range skills {
		if len(selected) > 0 && !selected[skill.ID] {
			continue
		}
		managed := skill.ManagedByDefault() || (options.Project && skill.Scope == model.ScopeRepo)
		if !managed {
			report.Skipped++
			continue
		}
		report.Managed++
		desired := resolver.desired(cfg, skill)
		actual := skill.ActualState()
		if actual == desired {
			continue
		}
		report.Changed++
		change := model.Change{SkillID: skill.ID, Name: skill.Name, Path: skill.Path, From: actual, To: desired}
		if options.DryRun {
			report.Changes = append(report.Changes, change)
			continue
		}

		entry, ok := localState.Entries[skill.Path]
		if !ok {
			snapshot, inspectErr := policy.Inspect(skill.PolicyPath)
			if inspectErr != nil {
				change.Message = inspectErr.Error()
				report.Conflicts++
				report.Changes = append(report.Changes, change)
				continue
			}
			entry = statestore.Entry{
				SkillID:               skill.ID,
				SkillPath:             skill.Path,
				SkillConfigName:       skill.ConfigName,
				PluginID:              skill.PluginID,
				PolicyPath:            skill.PolicyPath,
				PolicyFileExisted:     snapshot.FileExisted,
				OriginalPolicyPresent: snapshot.Present,
				OriginalPolicyValue:   snapshot.Value,
				OriginalEnabled:       skill.Enabled,
			}
		}

		if desired == model.StateDisabled {
			if client == nil {
				change.Message = "Codex app-server is required to disable a skill"
				report.Conflicts++
				report.Changes = append(report.Changes, change)
				continue
			}
			entry.ManagedEnabled = true
			entry.SkillConfigName = skill.ConfigName
			entry.LastSyncedAt = time.Now()
			localState.Entries[skill.Path] = entry
			if err := statestore.Save(m.StatePath, localState); err != nil {
				return report, err
			}
			if err := client.SetEnabled(skill.Path, skill.ConfigName, false); err != nil {
				change.Message = err.Error()
				report.Conflicts++
				report.Changes = append(report.Changes, change)
				continue
			}
		} else {
			if !skill.Enabled {
				if client == nil {
					change.Message = "Codex app-server is required to re-enable a skill"
					report.Conflicts++
					report.Changes = append(report.Changes, change)
					continue
				}
				entry.ManagedEnabled = true
				entry.SkillConfigName = skill.ConfigName
				entry.LastSyncedAt = time.Now()
				localState.Entries[skill.Path] = entry
				if err := statestore.Save(m.StatePath, localState); err != nil {
					return report, err
				}
				if err := client.SetEnabled(skill.Path, skill.ConfigName, true); err != nil {
					change.Message = err.Error()
					report.Conflicts++
					report.Changes = append(report.Changes, change)
					continue
				}
			}
			allow := desired == model.StateImplicit
			entry.ManagedPolicy = true
			entry.LastSyncedAt = time.Now()
			localState.Entries[skill.Path] = entry
			if err := statestore.Save(m.StatePath, localState); err != nil {
				return report, err
			}
			hash, err := policy.Set(skill.PolicyPath, allow)
			if err != nil {
				change.Message = err.Error()
				report.Conflicts++
				report.Changes = append(report.Changes, change)
				continue
			}
			entry.LastManagedHash = hash
		}
		entry.LastSyncedAt = time.Now()
		localState.Entries[skill.Path] = entry
		if err := statestore.Save(m.StatePath, localState); err != nil {
			return report, err
		}
		change.Applied = true
		report.Changes = append(report.Changes, change)
	}
	if report.Conflicts > 0 {
		return report, fmt.Errorf("%d skill changes could not be applied", report.Conflicts)
	}
	return report, nil
}

func (m Manager) Restore(ctx context.Context, selectors []string, all, dryRun bool) (RestoreReport, error) {
	report := RestoreReport{DryRun: dryRun}
	localState, err := statestore.LoadOrDefault(m.StatePath)
	if err != nil {
		return report, err
	}
	if len(localState.Entries) == 0 {
		return report, nil
	}
	selected := map[string]bool{}
	if !all {
		for _, selector := range selectors {
			selected[selector] = true
		}
	}
	var client codexSession
	needClient := false
	needPluginVerification := false
	for _, entry := range localState.Entries {
		matches := all || selected[entry.SkillID] || selected[entry.SkillPath]
		if !matches {
			continue
		}
		needClient = needClient || (!dryRun && entry.ManagedEnabled)
		needPluginVerification = needPluginVerification || stateEntryIsPlugin(entry)
	}
	var installed codex.InstalledPlugins
	if needClient || needPluginVerification {
		cfg, _, loadErr := config.LoadOrDefault(m.ConfigPath)
		if loadErr != nil {
			return report, loadErr
		}
		client, err = m.openCodexSession(ctx, cfg.Adapters.Codex.Command)
		if err != nil {
			if needPluginVerification {
				return report, &InventoryUnavailableError{Status: model.DiscoveryPartialFailure, Reason: err.Error()}
			}
			return report, err
		}
		defer client.Close()
		if needPluginVerification {
			installed, _, err = client.ListInstalledPlugins(m.CWD)
			if err != nil {
				status := model.DiscoveryPartialFailure
				reason := err.Error()
				if codex.IsMethodNotFound(err) {
					status = model.DiscoveryPartialUnsupported
					reason = pluginDiscoveryUpgradeMessage
				}
				return report, &InventoryUnavailableError{Status: status, Reason: reason}
			}
		}
	}
	for key, entry := range localState.Entries {
		if !all && !selected[entry.SkillID] && !selected[entry.SkillPath] {
			continue
		}
		if stateEntryIsPlugin(entry) {
			pluginID := stateEntryPluginID(entry)
			plugin, ok := installed.LookupID(pluginID)
			if !ok || !plugin.Installed || !plugin.Enabled {
				report.Conflicts = append(report.Conflicts, entry.SkillID+": plugin installation is not active and verified")
				continue
			}
			if !codex.PluginOwnsPath(plugin, entry.SkillPath, m.codexPluginCacheRoot()) {
				report.Conflicts = append(report.Conflicts, entry.SkillID+": restore snapshot does not belong to the active plugin package")
				continue
			}
		}
		if entry.ManagedPolicy && entry.LastManagedHash != "" {
			currentHash, hashErr := fileutil.HashFile(entry.PolicyPath)
			if hashErr != nil && !errors.Is(hashErr, os.ErrNotExist) {
				report.Conflicts = append(report.Conflicts, entry.SkillID+": "+hashErr.Error())
				continue
			}
			if currentHash != entry.LastManagedHash {
				report.Conflicts = append(report.Conflicts, entry.SkillID+": policy file changed outside skillctl")
				continue
			}
		}
		if dryRun {
			report.Restored = append(report.Restored, entry.SkillID)
			continue
		}
		if entry.ManagedEnabled {
			if err := client.SetEnabled(entry.SkillPath, entry.SkillConfigName, entry.OriginalEnabled); err != nil {
				report.Conflicts = append(report.Conflicts, entry.SkillID+": "+err.Error())
				continue
			}
		}
		if entry.ManagedPolicy {
			_, err := policy.Restore(entry.PolicyPath, policy.Snapshot{
				FileExisted: entry.PolicyFileExisted,
				Present:     entry.OriginalPolicyPresent,
				Value:       entry.OriginalPolicyValue,
			})
			if err != nil {
				report.Conflicts = append(report.Conflicts, entry.SkillID+": "+err.Error())
				continue
			}
		}
		delete(localState.Entries, key)
		if err := statestore.Save(m.StatePath, localState); err != nil {
			return report, err
		}
		report.Restored = append(report.Restored, entry.SkillID)
	}
	if !all && len(report.Restored) == 0 && len(report.Conflicts) == 0 {
		return report, errors.New("no managed state matched the requested selector")
	}
	if len(report.Conflicts) > 0 {
		return report, fmt.Errorf("%d restore conflicts", len(report.Conflicts))
	}
	sort.Strings(report.Restored)
	return report, nil
}

func (m Manager) discover(ctx context.Context, cfg config.Config) ([]model.Skill, model.DiscoveryReport, codexSession, error) {
	report := model.DiscoveryReport{Status: model.DiscoveryComplete}
	client, openErr := m.openCodexSession(ctx, cfg.Adapters.Codex.Command)
	if openErr != nil {
		report.Status = model.DiscoveryPartialFailure
		report.Warnings = append(report.Warnings, model.DiscoveryWarning{
			Code:    "plugin_discovery_failed",
			Message: "Codex app-server unavailable; plugin inventory omitted: " + openErr.Error(),
		})
		skills, err := m.partialFilesystemInventory(&report)
		return skills, report, nil, err
	}

	installed, installedWarnings, installedErr := client.ListInstalledPlugins(m.CWD)
	appendStringWarnings(&report, "plugin_marketplace_warning", installedWarnings)
	if installedErr != nil {
		if codex.IsMethodNotFound(installedErr) {
			report.Status = model.DiscoveryPartialUnsupported
			report.Warnings = append(report.Warnings, model.DiscoveryWarning{
				Code:    "plugin_discovery_unsupported",
				Message: pluginDiscoveryUpgradeMessage,
			})
		} else {
			report.Status = model.DiscoveryPartialFailure
			report.Warnings = append(report.Warnings, model.DiscoveryWarning{
				Code:    "plugin_discovery_failed",
				Message: "Codex installed-plugin discovery failed; plugin inventory omitted: " + installedErr.Error(),
			})
		}
		appSkills, skillWarnings, skillsErr := client.DiscoverSkills(m.CWD)
		appendStringWarnings(&report, "skill_discovery_warning", skillWarnings)
		if skillsErr != nil {
			report.Warnings = append(report.Warnings, model.DiscoveryWarning{Code: "skill_discovery_failed", Message: skillsErr.Error()})
			appSkills = nil
		}
		filesystemSkills, filesystemErr := m.partialFilesystemInventory(&report)
		if filesystemErr != nil {
			client.Close()
			return nil, report, nil, filesystemErr
		}
		return mergeNonPluginSkills(appSkills, filesystemSkills), report, client, nil
	}

	appSkills, skillWarnings, skillsErr := client.DiscoverSkills(m.CWD)
	appendStringWarnings(&report, "skill_discovery_warning", skillWarnings)
	if skillsErr != nil {
		report.Status = model.DiscoveryPartialFailure
		report.Warnings = append(report.Warnings, model.DiscoveryWarning{Code: "skill_discovery_failed", Message: skillsErr.Error()})
		filesystemSkills, filesystemErr := m.partialFilesystemInventory(&report)
		if filesystemErr != nil {
			client.Close()
			return nil, report, nil, filesystemErr
		}
		return filesystemSkills, report, client, nil
	}

	filesystemSkills, filesystemWarnings, filesystemErr := m.filesystemSkills()
	appendStringWarnings(&report, "filesystem_warning", filesystemWarnings)
	if filesystemErr != nil {
		report.Warnings = append(report.Warnings, model.DiscoveryWarning{Code: "filesystem_discovery_failed", Message: filesystemErr.Error()})
		filesystemSkills = nil
	}
	supplements, supplementWarnings := codex.DiscoverPluginSupplements(installed, m.codexPluginCacheRoot())
	report.Warnings = append(report.Warnings, supplementWarnings...)
	skills := reconcileInventory(appSkills, filesystemSkills, supplements, installed, m.codexPluginCacheRoot())
	return skills, report, client, nil
}

func (m Manager) openCodexSession(ctx context.Context, command string) (codexSession, error) {
	if m.openCodex != nil {
		return m.openCodex(ctx, command, m.CWD)
	}
	return codex.Open(ctx, command, m.CWD)
}

func (m Manager) filesystemSkills() ([]model.Skill, []string, error) {
	if m.discoverFilesystem != nil {
		return m.discoverFilesystem(m.CWD)
	}
	return codex.DiscoverFilesystem(m.CWD)
}

func (m Manager) codexPluginCacheRoot() string {
	if m.pluginCacheRoot != "" {
		return m.pluginCacheRoot
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "plugins", "cache")
}

func (m Manager) partialFilesystemInventory(report *model.DiscoveryReport) ([]model.Skill, error) {
	skills, warnings, err := m.filesystemSkills()
	appendStringWarnings(report, "filesystem_warning", warnings)
	if err != nil {
		return nil, err
	}
	return nonPluginSkills(skills), nil
}

func reconcileInventory(primary, filesystem, supplements []model.Skill, installed codex.InstalledPlugins, cacheRoot string) []model.Skill {
	primary = eligibleSkills(primary, installed, cacheRoot)
	filesystem = nonPluginSkills(filesystem)
	seenPath := map[string]bool{}
	seenID := map[string]bool{}
	result := make([]model.Skill, 0, len(primary)+len(filesystem)+len(supplements))
	for _, skill := range append(append(primary, supplements...), filesystem...) {
		if seenPath[skill.Path] || seenID[skill.ID] {
			continue
		}
		seenPath[skill.Path] = true
		seenID[skill.ID] = true
		result = append(result, skill)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func eligibleSkills(skills []model.Skill, installed codex.InstalledPlugins, cacheRoot string) []model.Skill {
	result := make([]model.Skill, 0, len(skills))
	for _, skill := range skills {
		if skill.Scope != model.ScopePlugin {
			result = append(result, skill)
			continue
		}
		plugin, ok := installed.LookupSource(skill.Source)
		if !ok || !plugin.Installed || !plugin.Enabled {
			continue
		}
		if !codex.PluginOwnsPath(plugin, skill.Path, cacheRoot) {
			continue
		}
		skill.PluginID = plugin.ID
		result = append(result, skill)
	}
	return result
}

func nonPluginSkills(skills []model.Skill) []model.Skill {
	result := make([]model.Skill, 0, len(skills))
	for _, skill := range skills {
		if skill.Scope != model.ScopePlugin {
			result = append(result, skill)
		}
	}
	return result
}

func mergeNonPluginSkills(primary, secondary []model.Skill) []model.Skill {
	return reconcileInventory(nonPluginSkills(primary), secondary, nil, codex.InstalledPlugins{}, "")
}

func appendStringWarnings(report *model.DiscoveryReport, code string, warnings []string) {
	for _, warning := range warnings {
		report.Warnings = append(report.Warnings, model.DiscoveryWarning{Code: code, Message: warning})
	}
}

type resolver struct {
	skills    []model.Skill
	nameCount map[string]int
}

type ambiguousSelectorError struct {
	selector string
	ids      []string
}

func (e ambiguousSelectorError) Error() string {
	return fmt.Sprintf("skill %q is ambiguous; use one of: %s", e.selector, strings.Join(e.ids, ", "))
}

func newResolver(skills []model.Skill) resolver {
	counts := map[string]int{}
	for _, skill := range skills {
		counts[skill.Name]++
	}
	return resolver{skills: skills, nameCount: counts}
}

func orphanRecords(profile config.Profile, localState statestore.State, resolver resolver) []model.OrphanRecord {
	var result []model.OrphanRecord
	policyGroups := []struct {
		selectors []string
		desired   model.InvocationState
	}{
		{selectors: profile.Implicit, desired: model.StateImplicit},
		{selectors: profile.Disabled, desired: model.StateDisabled},
	}
	for _, group := range policyGroups {
		for _, selector := range group.selectors {
			if _, err := resolver.resolve(selector); err != nil {
				var ambiguous ambiguousSelectorError
				if errors.As(err, &ambiguous) {
					continue
				}
				result = append(result, model.OrphanRecord{Kind: "policy", Selector: selector, Desired: group.desired})
			}
		}
	}
	for _, entry := range localState.Entries {
		if _, err := resolver.resolve(entry.SkillID); err == nil {
			continue
		}
		if _, err := resolver.resolve(entry.SkillPath); err == nil {
			continue
		}
		result = append(result, model.OrphanRecord{Kind: "restore", SkillID: entry.SkillID, SkillPath: entry.SkillPath})
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].Kind + ":" + result[i].Selector + ":" + result[i].SkillID
		right := result[j].Kind + ":" + result[j].Selector + ":" + result[j].SkillID
		return left < right
	})
	return result
}

func (r resolver) resolve(selector string) (model.Skill, error) {
	var matches []model.Skill
	for _, skill := range r.skills {
		if matchesSelector(selector, skill, r.nameCount) {
			matches = append(matches, skill)
		}
	}
	if len(matches) == 0 {
		return model.Skill{}, fmt.Errorf("skill %q was not found", selector)
	}
	if len(matches) > 1 {
		var ids []string
		for _, skill := range matches {
			ids = append(ids, skill.ID)
		}
		sort.Strings(ids)
		return model.Skill{}, ambiguousSelectorError{selector: selector, ids: ids}
	}
	return matches[0], nil
}

func (r resolver) desired(cfg config.Config, skill model.Skill) model.InvocationState {
	profile := cfg.Active()
	if containsSelector(profile.Disabled, skill, r.nameCount) {
		return model.StateDisabled
	}
	if containsSelector(profile.Implicit, skill, r.nameCount) {
		return model.StateImplicit
	}
	return cfg.Defaults.Invocation
}

func (r resolver) validateProfile(profile config.Profile) ([]string, error) {
	var warnings []string
	resolved := map[string]model.InvocationState{}
	groups := []struct {
		selectors []string
		state     model.InvocationState
	}{
		{selectors: profile.Implicit, state: model.StateImplicit},
		{selectors: profile.Disabled, state: model.StateDisabled},
	}
	for _, group := range groups {
		for _, selector := range group.selectors {
			skill, err := r.resolve(selector)
			if err != nil {
				var ambiguous ambiguousSelectorError
				if errors.As(err, &ambiguous) {
					return warnings, err
				}
				warnings = append(warnings, err.Error())
				continue
			}
			if previous, ok := resolved[skill.ID]; ok && previous != group.state {
				return warnings, fmt.Errorf("skill %s appears in both implicit and disabled policy lists", skill.ID)
			}
			resolved[skill.ID] = group.state
		}
	}
	return warnings, nil
}

func containsSelector(selectors []string, skill model.Skill, counts map[string]int) bool {
	for _, selector := range selectors {
		if matchesSelector(selector, skill, counts) {
			return true
		}
	}
	return false
}

func matchesSelector(selector string, skill model.Skill, counts map[string]int) bool {
	if selector == skill.ID || selector == skill.Path || selector == skill.ConfigName {
		return true
	}
	if selector == skill.Name && counts[skill.Name] == 1 {
		return true
	}
	for _, alias := range skill.Aliases {
		if selector == alias {
			return true
		}
	}
	return false
}

func selectionSet(selectors []string) map[string]bool {
	result := map[string]bool{}
	for _, selector := range selectors {
		result[selector] = true
	}
	return result
}

func partialSyncAllowed(options SyncOptions, skills []model.Skill) bool {
	if !options.allowPartialNonPlugin || len(options.Selectors) == 0 {
		return false
	}
	selected := selectionSet(options.Selectors)
	found := map[string]bool{}
	for _, skill := range skills {
		if !selected[skill.ID] {
			continue
		}
		if skill.Scope == model.ScopePlugin {
			return false
		}
		found[skill.ID] = true
	}
	return len(found) == len(selected)
}

func inventoryUnavailable(discovery model.DiscoveryReport) error {
	reason := "plugin installation state could not be verified"
	if len(discovery.Warnings) > 0 {
		reason = discovery.Warnings[0].Message
	}
	return &InventoryUnavailableError{Status: discovery.Status, Reason: reason}
}

func stateEntryIsPlugin(entry statestore.Entry) bool {
	return entry.PluginID != "" || strings.HasPrefix(entry.SkillID, "codex:plugin:")
}

func stateEntryPluginID(entry statestore.Entry) string {
	if entry.PluginID != "" {
		return entry.PluginID
	}
	parts := strings.Split(entry.SkillID, ":")
	if len(parts) >= 5 && parts[0] == "codex" && parts[1] == "plugin" {
		return parts[3] + "@" + parts[2]
	}
	return ""
}

func configHint(path string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return &config.Error{Err: fmt.Errorf("config not found at %s; run skillctl init first", path)}
	}
	return err
}
