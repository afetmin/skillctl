package service

import (
	"context"
	"errors"
	"fmt"
	"os"
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
}

type SyncOptions struct {
	DryRun    bool
	Project   bool
	Selectors []string
}

type SkillStatus struct {
	model.Skill
	Actual  model.InvocationState `json:"actual"`
	Desired model.InvocationState `json:"desired"`
	Managed bool                  `json:"managed"`
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

func (m Manager) Discover(ctx context.Context) ([]model.Skill, []string, error) {
	cfg, _, err := config.LoadOrDefault(m.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	skills, warnings, client, err := m.discover(ctx, cfg)
	if client != nil {
		defer client.Close()
	}
	return skills, warnings, err
}

func (m Manager) List(ctx context.Context, project bool) ([]SkillStatus, []string, error) {
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		return nil, nil, configHint(m.ConfigPath, err)
	}
	skills, warnings, client, err := m.discover(ctx, cfg)
	if client != nil {
		defer client.Close()
	}
	if err != nil {
		return nil, warnings, err
	}
	resolver := newResolver(skills)
	profileWarnings, profileErr := resolver.validateProfile(cfg.Active())
	warnings = append(warnings, profileWarnings...)
	if profileErr != nil {
		return nil, warnings, profileErr
	}
	var result []SkillStatus
	for _, skill := range skills {
		managed := skill.ManagedByDefault() || (project && skill.Scope == model.ScopeRepo)
		actual := skill.ActualState()
		desired := actual
		if managed {
			desired = resolver.desired(cfg, skill)
		}
		result = append(result, SkillStatus{
			Skill:   skill,
			Actual:  actual,
			Desired: desired,
			Managed: managed,
		})
	}
	return result, warnings, nil
}

func (m Manager) Resolve(ctx context.Context, selector string) (model.Skill, error) {
	skills, _, err := m.Discover(ctx)
	if err != nil {
		return model.Skill{}, err
	}
	return newResolver(skills).resolve(selector)
}

func (m Manager) Set(ctx context.Context, selector string, desired model.InvocationState, noSync, project bool) (model.Skill, *model.SyncReport, error) {
	if !desired.Valid() {
		return model.Skill{}, nil, fmt.Errorf("invalid invocation state %q", desired)
	}
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		return model.Skill{}, nil, configHint(m.ConfigPath, err)
	}
	skills, _, client, err := m.discover(ctx, cfg)
	if client != nil {
		_ = client.Close()
	}
	if err != nil {
		return model.Skill{}, nil, err
	}
	skill, err := newResolver(skills).resolve(selector)
	if err != nil {
		return model.Skill{}, nil, err
	}
	if skill.Scope == model.ScopeSystem || skill.Scope == model.ScopeAdmin {
		return model.Skill{}, nil, fmt.Errorf("%s is a %s skill and is outside skillctl management", skill.ID, skill.Scope)
	}
	if skill.Scope == model.ScopeRepo && !project {
		return model.Skill{}, nil, errors.New("project skills require --project")
	}
	cfg.SetState(skill.ID, skill.Name, desired)
	if err := config.Save(m.ConfigPath, cfg); err != nil {
		return model.Skill{}, nil, err
	}
	if noSync {
		return skill, nil, nil
	}
	report, err := m.Sync(ctx, SyncOptions{Project: project, Selectors: []string{skill.ID}})
	return skill, &report, err
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
	skills, warnings, client, err := m.discover(ctx, cfg)
	if client != nil {
		defer client.Close()
	}
	report.Warnings = append(report.Warnings, warnings...)
	if err != nil {
		return report, err
	}
	report.Scanned = len(skills)
	resolver := newResolver(skills)
	profileWarnings, profileErr := resolver.validateProfile(cfg.Active())
	report.Warnings = append(report.Warnings, profileWarnings...)
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
			entry.LastSyncedAt = time.Now()
			localState.Entries[skill.Path] = entry
			if err := statestore.Save(m.StatePath, localState); err != nil {
				return report, err
			}
			if err := client.SetEnabled(skill.Path, false); err != nil {
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
				entry.LastSyncedAt = time.Now()
				localState.Entries[skill.Path] = entry
				if err := statestore.Save(m.StatePath, localState); err != nil {
					return report, err
				}
				if err := client.SetEnabled(skill.Path, true); err != nil {
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
	var client *codex.Client
	if !dryRun {
		needClient := false
		for _, entry := range localState.Entries {
			matches := all || selected[entry.SkillID] || selected[entry.SkillPath]
			if matches && entry.ManagedEnabled {
				needClient = true
				break
			}
		}
		if needClient {
			cfg, _, loadErr := config.LoadOrDefault(m.ConfigPath)
			if loadErr != nil {
				return report, loadErr
			}
			client, err = codex.Open(ctx, cfg.Adapters.Codex.Command, m.CWD)
			if err != nil {
				return report, err
			}
			defer client.Close()
		}
	}
	for key, entry := range localState.Entries {
		if !all && !selected[entry.SkillID] && !selected[entry.SkillPath] {
			continue
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
			if err := client.SetEnabled(entry.SkillPath, entry.OriginalEnabled); err != nil {
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

func (m Manager) discover(ctx context.Context, cfg config.Config) ([]model.Skill, []string, *codex.Client, error) {
	client, err := codex.Open(ctx, cfg.Adapters.Codex.Command, m.CWD)
	if err == nil {
		skills, warnings, discoverErr := codex.Discover(client, m.CWD)
		if discoverErr == nil {
			filesystemSkills, filesystemWarnings, filesystemErr := codex.DiscoverFilesystem(m.CWD)
			warnings = append(warnings, filesystemWarnings...)
			if filesystemErr != nil {
				warnings = append(warnings, "filesystem inventory: "+filesystemErr.Error())
			} else {
				skills = mergeSkills(skills, filesystemSkills)
			}
			return skills, warnings, client, nil
		}
		client.Close()
		err = discoverErr
	}
	skills, warnings, fallbackErr := codex.DiscoverFilesystem(m.CWD)
	warnings = append(warnings, "Codex app-server unavailable; using filesystem fallback: "+err.Error())
	if fallbackErr != nil {
		return nil, warnings, nil, fallbackErr
	}
	return skills, warnings, nil, nil
}

func mergeSkills(primary, secondary []model.Skill) []model.Skill {
	sort.SliceStable(secondary, func(i, j int) bool {
		return skillModTime(secondary[i]).After(skillModTime(secondary[j]))
	})
	seenPath := map[string]bool{}
	seenID := map[string]bool{}
	result := make([]model.Skill, 0, len(primary)+len(secondary))
	for _, skill := range append(primary, secondary...) {
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

func skillModTime(skill model.Skill) time.Time {
	info, err := os.Stat(skill.Path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
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

func (r resolver) resolve(selector string) (model.Skill, error) {
	var matches []model.Skill
	for _, skill := range r.skills {
		if skill.ID == selector || skill.Path == selector || skill.Name == selector {
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
		if selector == skill.ID || (selector == skill.Name && counts[skill.Name] == 1) {
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

func configHint(path string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return &config.Error{Err: fmt.Errorf("config not found at %s; run skillctl init first", path)}
	}
	return err
}
