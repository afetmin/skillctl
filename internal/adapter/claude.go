package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"skillctl/internal/claude"
	"skillctl/internal/model"
	"skillctl/internal/skillfs"
	statestore "skillctl/internal/state"
)

type claudeAdapter struct {
	cwd string
}

func newClaude(cwd string) *claudeAdapter {
	return &claudeAdapter{cwd: cwd}
}

func (a *claudeAdapter) Agent() model.Agent {
	return model.AgentClaude
}

func (a *claudeAdapter) States() []model.InvocationState {
	return model.States(model.AgentClaude)
}

func (a *claudeAdapter) Discover(_ context.Context) ([]model.Skill, model.DiscoveryReport, error) {
	return claude.Discover(a.cwd)
}

func (a *claudeAdapter) NeedsApply(skill model.Skill, desired model.InvocationState) (bool, error) {
	if conflict, err := claude.HigherPriorityConflict(a.cwd, skill, desired); err != nil {
		return false, err
	} else if conflict != "" {
		return false, errors.New(conflict)
	}
	path, err := claude.TargetSettingsPath(a.cwd, skill)
	if err != nil {
		return false, err
	}
	current, present, err := claude.ReadOverride(path, skill.Name)
	if err != nil {
		return false, err
	}
	target, err := claude.OverrideForState(desired)
	if err != nil {
		return false, err
	}
	return !present || current != target, nil
}

func (a *claudeAdapter) Prepare(skill model.Skill, desired model.InvocationState, existing *statestore.Entry) (statestore.Entry, error) {
	if !model.ValidState(model.AgentClaude, desired) {
		return statestore.Entry{}, fmt.Errorf("Claude does not support invocation state %q", desired)
	}
	if skill.ReadOnly {
		return statestore.Entry{}, fmt.Errorf("%s is shadowed by %s and is read-only", skill.ID, skill.BlockedBy)
	}
	if conflict, err := claude.HigherPriorityConflict(a.cwd, skill, desired); err != nil {
		return statestore.Entry{}, err
	} else if conflict != "" {
		return statestore.Entry{}, errors.New(conflict)
	}
	path, err := claude.TargetSettingsPath(a.cwd, skill)
	if err != nil {
		return statestore.Entry{}, err
	}
	current, present, err := claude.ReadOverride(path, skill.Name)
	if err != nil {
		return statestore.Entry{}, err
	}
	entry := statestore.Entry{}
	if existing == nil {
		entry = statestore.Entry{
			Agent:                   model.AgentClaude,
			SkillID:                 skill.ID,
			SkillPath:               skill.Path,
			SkillConfigName:         skill.Name,
			Scope:                   skill.Scope,
			OverridePath:            path,
			OriginalOverridePresent: present,
			OriginalOverrideValue:   current,
		}
	} else {
		entry = *existing
		if entry.OverridePath != path {
			return statestore.Entry{}, errors.New("Claude settings target changed since the recovery snapshot was created")
		}
		if entry.LastManagedOverride != "" && (!present || current != entry.LastManagedOverride) {
			return statestore.Entry{}, errors.New("Claude skill override changed outside skillctl")
		}
		if entry.LastManagedOverride == "" && (present != entry.OriginalOverridePresent || present && current != entry.OriginalOverrideValue) {
			return statestore.Entry{}, errors.New("Claude skill override changed before skillctl completed its first write")
		}
	}
	entry.LastSyncedAt = time.Now()
	return entry, nil
}

func (a *claudeAdapter) Apply(skill model.Skill, desired model.InvocationState, entry statestore.Entry) (statestore.Entry, error) {
	value, err := claude.OverrideForState(desired)
	if err != nil {
		return entry, err
	}
	if err := claude.WriteOverride(entry.OverridePath, skill.Name, &value); err != nil {
		return entry, err
	}
	entry.LastManagedOverride = value
	entry.LastSyncedAt = time.Now()
	return entry, nil
}

func (a *claudeAdapter) CheckRestore(entry statestore.Entry) error {
	current, present, err := claude.ReadOverride(entry.OverridePath, entry.SkillConfigName)
	if err != nil {
		return err
	}
	if entry.LastManagedOverride == "" {
		if present != entry.OriginalOverridePresent || present && current != entry.OriginalOverrideValue {
			return errors.New("Claude skill override changed outside skillctl")
		}
		return nil
	}
	if !present || current != entry.LastManagedOverride {
		return errors.New("Claude skill override changed outside skillctl")
	}
	return nil
}

func (a *claudeAdapter) Restore(entry statestore.Entry) error {
	if err := a.CheckRestore(entry); err != nil {
		return err
	}
	if entry.LastManagedOverride == "" {
		return nil
	}
	if !entry.OriginalOverridePresent {
		return claude.WriteOverride(entry.OverridePath, entry.SkillConfigName, nil)
	}
	value := entry.OriginalOverrideValue
	return claude.WriteOverride(entry.OverridePath, entry.SkillConfigName, &value)
}

func (a *claudeAdapter) Delete(skill model.Skill) error {
	roots, err := claude.Roots(a.cwd)
	if err != nil {
		return err
	}
	return skillfs.Delete(skill, roots)
}

func (a *claudeAdapter) Close() error {
	return nil
}
