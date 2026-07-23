package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"skillctl/internal/adapter"
	"skillctl/internal/config"
	"skillctl/internal/model"
	statestore "skillctl/internal/state"
)

type Manager struct {
	Agent      model.Agent
	ConfigPath string
	StateDir   string
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
	Journal *statestore.Entry     `json:"-"`
}

type RestoreReport struct {
	Agent     model.Agent `json:"agent"`
	Restored  []string    `json:"restored"`
	Skipped   []string    `json:"skipped,omitempty"`
	Conflicts []string    `json:"conflicts,omitempty"`
	DryRun    bool        `json:"dry_run"`
}

func (m Manager) StatePath() string {
	return statestore.Path(m.StateDir, m.Agent)
}

func (m Manager) ValidStates() []model.InvocationState {
	return model.States(m.Agent)
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
	active, err := m.openAdapter(cfg)
	if err != nil {
		return nil, model.DiscoveryReport{}, err
	}
	defer active.Close()
	skills, report, err := active.Discover(ctx)
	return supportedInventory(m.Agent, skills), report, err
}

func (m Manager) List(ctx context.Context, project bool) ([]SkillStatus, model.DiscoveryReport, error) {
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		return nil, model.DiscoveryReport{}, configHint(m.ConfigPath, err)
	}
	agentConfig, err := cfg.Agent(m.Agent)
	if err != nil {
		return nil, model.DiscoveryReport{}, err
	}
	active, err := m.openAdapter(cfg)
	if err != nil {
		return nil, model.DiscoveryReport{}, err
	}
	defer active.Close()
	skills, discovery, err := active.Discover(ctx)
	if err != nil {
		return nil, discovery, err
	}
	if m.Agent == model.AgentCodex && discovery.Status == model.DiscoveryPartialFailure {
		return []SkillStatus{}, discovery, nil
	}
	skills = supportedInventory(m.Agent, skills)
	resolver := newResolver(skills)
	profileWarnings, profileErr := resolver.validateProfile(agentConfig.Profiles[agentConfig.ActiveProfile])
	for _, warning := range profileWarnings {
		discovery.Warnings = append(discovery.Warnings, model.DiscoveryWarning{Code: "orphaned_policy", Message: warning})
	}
	if profileErr != nil {
		return nil, discovery, profileErr
	}
	localState, err := statestore.LoadOrDefault(m.StatePath(), m.Agent)
	if err != nil {
		return nil, discovery, err
	}
	result := make([]SkillStatus, 0, len(skills))
	for _, skill := range skills {
		managed := !skill.ReadOnly && (skill.ManagedByDefault() || (project && skill.Scope == model.ScopeRepo))
		actual := skill.ActualState()
		desired := actual
		if managed {
			desired = resolver.desired(agentConfig, skill)
		}
		if skill.Scope == model.ScopeRepo && !project {
			skill.ReadOnly = true
			if skill.BlockedBy == "" {
				skill.BlockedBy = "project management is disabled"
			}
		}
		status := SkillStatus{Skill: skill, Actual: actual, Desired: desired, Managed: managed}
		if entry, ok := localState.Entries[skill.Path]; ok {
			entryCopy := entry
			status.Journal = &entryCopy
		}
		result = append(result, status)
	}
	return result, discovery, nil
}

func (m Manager) Resolve(ctx context.Context, selector string) (model.Skill, error) {
	skills, discovery, err := m.Discover(ctx)
	if err != nil {
		return model.Skill{}, err
	}
	if m.Agent == model.AgentCodex && discovery.Status == model.DiscoveryPartialFailure {
		return model.Skill{}, errors.New("Codex inventory is unavailable because enabled state could not be verified")
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
	active, err := m.openAdapter(cfg)
	if err != nil {
		return nil, nil, err
	}
	discovered, discovery, err := active.Discover(ctx)
	_ = active.Close()
	if err != nil {
		return nil, nil, err
	}
	if m.Agent == model.AgentCodex && discovery.Status == model.DiscoveryPartialFailure {
		return nil, nil, errors.New("Codex inventory is unavailable because enabled state could not be verified")
	}
	discovered = supportedInventory(m.Agent, discovered)
	resolver := newResolver(discovered)
	resolved := make([]model.Skill, 0, len(changes))
	desiredByID := map[string]model.InvocationState{}
	for selector, desired := range changes {
		if !model.ValidState(m.Agent, desired) {
			return nil, nil, fmt.Errorf("%s does not support invocation state %q", m.Agent, desired)
		}
		skill, resolveErr := resolver.resolve(selector)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		if skill.ReadOnly {
			return nil, nil, fmt.Errorf("%s is read-only: shadowed by %s", skill.ID, skill.BlockedBy)
		}
		if skill.Scope != model.ScopeUser && skill.Scope != model.ScopeRepo {
			return nil, nil, fmt.Errorf("%s is outside %s personal and project Skill management", skill.ID, m.Agent)
		}
		if skill.Scope == model.ScopeRepo && !project {
			return nil, nil, errors.New("project skills require --project")
		}
		desiredByID[skill.ID] = desired
		resolved = append(resolved, skill)
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].ID < resolved[j].ID })
	for _, skill := range resolved {
		if err := cfg.SetState(m.Agent, skill.ID, skill.Name, desiredByID[skill.ID], skill.Aliases...); err != nil {
			return nil, nil, err
		}
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
	report, err := m.Sync(ctx, SyncOptions{Project: project, Selectors: selectors})
	return resolved, &report, err
}

func (m Manager) Sync(ctx context.Context, options SyncOptions) (model.SyncReport, error) {
	report := model.SyncReport{Agent: m.Agent, DryRun: options.DryRun, At: time.Now(), Changes: []model.Change{}}
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		return report, configHint(m.ConfigPath, err)
	}
	agentConfig, err := cfg.Agent(m.Agent)
	if err != nil {
		return report, err
	}
	localState, err := statestore.LoadOrDefault(m.StatePath(), m.Agent)
	if err != nil {
		return report, err
	}
	active, err := m.openAdapter(cfg)
	if err != nil {
		return report, err
	}
	defer active.Close()
	skills, discovery, err := active.Discover(ctx)
	report.Discovery = discovery
	report.Warnings = append(report.Warnings, discovery.Warnings...)
	if err != nil {
		return report, err
	}
	if m.Agent == model.AgentCodex && discovery.Status == model.DiscoveryPartialFailure {
		return report, errors.New("Codex inventory is unavailable because enabled state could not be verified")
	}
	skills = supportedInventory(m.Agent, skills)
	report.Scanned = len(skills)
	resolver := newResolver(skills)
	profile := agentConfig.Profiles[agentConfig.ActiveProfile]
	report.Orphans = orphanRecords(m.Agent, profile, localState, resolver)
	profileWarnings, profileErr := resolver.validateProfile(profile)
	for _, warning := range profileWarnings {
		item := model.DiscoveryWarning{Code: "orphaned_policy", Message: warning}
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
		managed := !skill.ReadOnly && (skill.ManagedByDefault() || (options.Project && skill.Scope == model.ScopeRepo))
		if !managed {
			report.Skipped++
			desired := resolver.desired(agentConfig, skill)
			actual := skill.ActualState()
			if actual != desired {
				reason := "project management is disabled"
				blocked := skill.ReadOnly && (skill.Scope != model.ScopeRepo || options.Project)
				if blocked {
					reason = "read-only"
					if skill.BlockedBy != "" {
						reason += ": shadowed by " + skill.BlockedBy
					}
				}
				change := model.Change{
					Agent:   m.Agent,
					SkillID: skill.ID,
					Name:    skill.Name,
					Path:    skill.Path,
					From:    actual,
					To:      desired,
					Reason:  reason,
				}
				report.Changes = append(report.Changes, change)
				if blocked {
					report.Changed++
					report.Conflicts++
					report.ConflictedChanges = append(report.ConflictedChanges, change)
				} else {
					report.SkippedChanges = append(report.SkippedChanges, change)
				}
			}
			continue
		}
		report.Managed++
		desired := resolver.desired(agentConfig, skill)
		actual := skill.ActualState()
		needsApply, needsApplyErr := active.NeedsApply(skill, desired)
		if needsApplyErr != nil {
			change := model.Change{
				Agent:   m.Agent,
				SkillID: skill.ID,
				Name:    skill.Name,
				Path:    skill.Path,
				From:    actual,
				To:      desired,
				Reason:  needsApplyErr.Error(),
			}
			report.Changed++
			report.Conflicts++
			report.Skipped++
			report.Changes = append(report.Changes, change)
			report.ConflictedChanges = append(report.ConflictedChanges, change)
			continue
		}
		if !needsApply {
			continue
		}
		report.Changed++
		change := model.Change{
			Agent:   m.Agent,
			SkillID: skill.ID,
			Name:    skill.Name,
			Path:    skill.Path,
			From:    actual,
			To:      desired,
		}
		var existing *statestore.Entry
		if entry, ok := localState.Entries[skill.Path]; ok {
			entryCopy := entry
			existing = &entryCopy
		}
		entry, prepareErr := active.Prepare(skill, desired, existing)
		if prepareErr != nil {
			change.Reason = prepareErr.Error()
			report.Conflicts++
			report.Skipped++
			report.Changes = append(report.Changes, change)
			report.ConflictedChanges = append(report.ConflictedChanges, change)
			continue
		}
		if options.DryRun {
			report.Changes = append(report.Changes, change)
			continue
		}
		localState.Entries[skill.Path] = entry
		if err := statestore.Save(m.StatePath(), localState); err != nil {
			return report, err
		}
		entry, applyErr := active.Apply(skill, desired, entry)
		if applyErr != nil {
			change.Reason = applyErr.Error()
			report.Conflicts++
			report.Skipped++
			report.Changes = append(report.Changes, change)
			report.ConflictedChanges = append(report.ConflictedChanges, change)
			continue
		}
		localState.Entries[skill.Path] = entry
		if err := statestore.Save(m.StatePath(), localState); err != nil {
			return report, err
		}
		change.Applied = true
		report.Changes = append(report.Changes, change)
		report.AppliedChanges = append(report.AppliedChanges, change)
	}
	if report.Conflicts > 0 {
		return report, fmt.Errorf("%d skill changes could not be applied", report.Conflicts)
	}
	return report, nil
}

func (m Manager) Restore(ctx context.Context, selectors []string, all, dryRun, project bool) (RestoreReport, error) {
	report := RestoreReport{Agent: m.Agent, DryRun: dryRun}
	localState, err := statestore.LoadOrDefault(m.StatePath(), m.Agent)
	if err != nil {
		return report, err
	}
	if len(localState.Entries) == 0 {
		return report, nil
	}
	cfg, _, err := config.LoadOrDefault(m.ConfigPath)
	if err != nil {
		return report, err
	}
	active, err := m.openAdapter(cfg)
	if err != nil {
		return report, err
	}
	defer active.Close()
	if _, _, err := active.Discover(ctx); err != nil {
		return report, err
	}
	selected := selectionSet(selectors)
	for key, entry := range localState.Entries {
		if !all && !selected[entry.SkillID] && !selected[entry.SkillPath] {
			continue
		}
		if entry.Scope == model.ScopeRepo && !project {
			report.Conflicts = append(report.Conflicts, entry.SkillID+": project restore requires --project")
			continue
		}
		if err := active.CheckRestore(entry); err != nil {
			report.Conflicts = append(report.Conflicts, entry.SkillID+": "+err.Error())
			continue
		}
		if dryRun {
			report.Restored = append(report.Restored, entry.SkillID)
			continue
		}
		if err := active.Restore(entry); err != nil {
			report.Conflicts = append(report.Conflicts, entry.SkillID+": "+err.Error())
			continue
		}
		delete(localState.Entries, key)
		if err := statestore.Save(m.StatePath(), localState); err != nil {
			return report, err
		}
		report.Restored = append(report.Restored, entry.SkillID)
	}
	if !all && len(report.Restored) == 0 && len(report.Conflicts) == 0 {
		return report, errors.New("no managed state matched the requested selector")
	}
	sort.Strings(report.Restored)
	if len(report.Conflicts) > 0 {
		return report, fmt.Errorf("%d restore conflicts", len(report.Conflicts))
	}
	return report, nil
}

func (m Manager) DeleteSkill(skill model.Skill, project bool) error {
	if m.Agent == "" {
		m.Agent = skill.Agent
		if m.Agent == "" {
			m.Agent = model.AgentCodex
		}
	}
	if skill.Agent != "" && skill.Agent != m.Agent {
		return fmt.Errorf("%s belongs to %s, not the active %s Agent", skill.ID, skill.Agent, m.Agent)
	}
	if skill.ReadOnly {
		return fmt.Errorf("%s is read-only: shadowed by %s", skill.ID, skill.BlockedBy)
	}
	if skill.Scope == model.ScopeRepo && !project {
		return errors.New("project skills require --project")
	}
	cfg, _, err := config.LoadOrDefault(m.ConfigPath)
	if err != nil {
		return err
	}
	active, err := m.openAdapter(cfg)
	if err != nil {
		return err
	}
	defer active.Close()
	return active.Delete(skill)
}

func (m Manager) openAdapter(cfg config.Config) (adapter.Adapter, error) {
	if !m.Agent.Valid() {
		return nil, errors.New("an active Agent is required")
	}
	value, err := cfg.Agent(m.Agent)
	if err != nil {
		return nil, err
	}
	return adapter.New(m.Agent, value.Command, m.CWD)
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

func (r resolver) desired(cfg config.AgentConfig, skill model.Skill) model.InvocationState {
	profile := cfg.Profiles[cfg.ActiveProfile]
	if containsSelector(profile.Disabled, skill, r.nameCount) {
		return model.StateDisabled
	}
	if containsSelector(profile.NameOnly, skill, r.nameCount) {
		return model.StateNameOnly
	}
	if containsSelector(profile.Manual, skill, r.nameCount) {
		return model.StateManual
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
		{selectors: profile.NameOnly, state: model.StateNameOnly},
		{selectors: profile.Manual, state: model.StateManual},
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
				return warnings, fmt.Errorf("skill %s appears in both %s and %s policy lists", skill.ID, previous, group.state)
			}
			resolved[skill.ID] = group.state
		}
	}
	return warnings, nil
}

func orphanRecords(agent model.Agent, profile config.Profile, localState statestore.State, resolver resolver) []model.OrphanRecord {
	var result []model.OrphanRecord
	groups := []struct {
		selectors []string
		desired   model.InvocationState
	}{
		{selectors: profile.Implicit, desired: model.StateImplicit},
		{selectors: profile.NameOnly, desired: model.StateNameOnly},
		{selectors: profile.Manual, desired: model.StateManual},
		{selectors: profile.Disabled, desired: model.StateDisabled},
	}
	for _, group := range groups {
		for _, selector := range group.selectors {
			if _, err := resolver.resolve(selector); err != nil {
				var ambiguous ambiguousSelectorError
				if errors.As(err, &ambiguous) {
					continue
				}
				result = append(result, model.OrphanRecord{Agent: agent, Kind: "policy", Selector: selector, Desired: group.desired})
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
		result = append(result, model.OrphanRecord{Agent: agent, Kind: "restore", SkillID: entry.SkillID, SkillPath: entry.SkillPath})
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].Kind + ":" + result[i].Selector + ":" + result[i].SkillID
		right := result[j].Kind + ":" + result[j].Selector + ":" + result[j].SkillID
		return left < right
	})
	return result
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
	if selector == skill.ID || selector == skill.Path {
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

func supportedInventory(agent model.Agent, skills []model.Skill) []model.Skill {
	result := make([]model.Skill, 0, len(skills))
	for _, skill := range skills {
		if skill.Agent != agent {
			continue
		}
		if skill.Scope != model.ScopeUser && skill.Scope != model.ScopeRepo {
			continue
		}
		result = append(result, skill)
	}
	return result
}

func configHint(path string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return &config.Error{Err: fmt.Errorf("config not found at %s; run skillctl init first", path)}
	}
	return err
}
