package adapter

import (
	"context"
	"errors"
	"os"
	"time"

	"skillctl/internal/codex"
	"skillctl/internal/fileutil"
	"skillctl/internal/model"
	"skillctl/internal/policy"
	"skillctl/internal/skillfs"
	statestore "skillctl/internal/state"
)

type codexAdapter struct {
	command            string
	cwd                string
	client             *codex.Client
	enablementVerified bool
}

func newCodex(command, cwd string) *codexAdapter {
	return &codexAdapter{command: command, cwd: cwd}
}

func (a *codexAdapter) Agent() model.Agent {
	return model.AgentCodex
}

func (a *codexAdapter) States() []model.InvocationState {
	return model.States(model.AgentCodex)
}

func (a *codexAdapter) Discover(ctx context.Context) ([]model.Skill, model.DiscoveryReport, error) {
	a.enablementVerified = false
	skills, report, err := codex.DiscoverSupportedFilesystem(a.cwd)
	if err != nil {
		return nil, report, err
	}
	client, err := codex.Open(ctx, a.command, a.cwd)
	if err != nil {
		report.Status = model.DiscoveryPartialFailure
		report.Warnings = append(report.Warnings, model.DiscoveryWarning{
			Code:    "codex_app_server_unavailable",
			Message: "Codex app-server is unavailable; inventory was withheld because enabled state cannot be verified: " + err.Error(),
		})
		return nil, report, nil
	}
	a.client = client
	enablement, err := client.ReadSkillEnablement(a.cwd)
	if err != nil {
		report.Status = model.DiscoveryPartialFailure
		report.Warnings = append(report.Warnings, model.DiscoveryWarning{
			Code:    "codex_enablement_unavailable",
			Message: "Codex inventory was withheld because skill enablement could not be read: " + err.Error(),
		})
		return nil, report, nil
	}
	a.enablementVerified = true
	return codex.ApplyEnablement(skills, enablement), report, nil
}

func (a *codexAdapter) NeedsApply(skill model.Skill, desired model.InvocationState) (bool, error) {
	if !a.enablementVerified {
		return false, errors.New("Codex skill enablement is unavailable; refusing to change an unverified state")
	}
	return skill.ActualState() != desired, nil
}

func (a *codexAdapter) Prepare(skill model.Skill, desired model.InvocationState, existing *statestore.Entry) (statestore.Entry, error) {
	if !model.ValidState(model.AgentCodex, desired) {
		return statestore.Entry{}, errors.New("Codex does not support name-only")
	}
	if !a.enablementVerified {
		return statestore.Entry{}, errors.New("Codex skill enablement is unavailable; refusing to change an unverified state")
	}
	if a.client == nil && desired == model.StateDisabled {
		return statestore.Entry{}, errors.New("Codex app-server is required to disable a skill")
	}
	if a.client == nil && !skill.Enabled {
		return statestore.Entry{}, errors.New("Codex app-server is required to re-enable a skill")
	}
	entry := statestore.Entry{}
	if existing != nil {
		entry = *existing
		if entry.ManagedPolicy && entry.LastManagedHash != "" {
			currentHash, err := fileutil.HashFile(entry.PolicyPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return statestore.Entry{}, errors.New("Codex policy file changed outside skillctl")
				}
				return statestore.Entry{}, err
			}
			if currentHash != entry.LastManagedHash {
				return statestore.Entry{}, errors.New("Codex policy file changed outside skillctl")
			}
		}
	} else {
		snapshot, err := policy.Inspect(skill.PolicyPath)
		if err != nil {
			return statestore.Entry{}, err
		}
		entry = statestore.Entry{
			Agent:                 model.AgentCodex,
			SkillID:               skill.ID,
			SkillPath:             skill.Path,
			Scope:                 skill.Scope,
			PolicyPath:            skill.PolicyPath,
			PolicyFileExisted:     snapshot.FileExisted,
			OriginalPolicyPresent: snapshot.Present,
			OriginalPolicyValue:   snapshot.Value,
			OriginalEnabled:       skill.Enabled,
		}
	}
	entry.ManagedEnabled = entry.ManagedEnabled || desired == model.StateDisabled || !skill.Enabled
	entry.ManagedPolicy = entry.ManagedPolicy || desired != model.StateDisabled
	entry.LastSyncedAt = time.Now()
	return entry, nil
}

func (a *codexAdapter) Apply(skill model.Skill, desired model.InvocationState, entry statestore.Entry) (statestore.Entry, error) {
	if desired == model.StateDisabled {
		if a.client == nil {
			return entry, errors.New("Codex app-server is required to disable a skill")
		}
		if err := a.client.SetEnabled(skill.Path, false); err != nil {
			return entry, err
		}
		return entry, nil
	}
	if !skill.Enabled {
		if a.client == nil {
			return entry, errors.New("Codex app-server is required to re-enable a skill")
		}
		if err := a.client.SetEnabled(skill.Path, true); err != nil {
			return entry, err
		}
	}
	hash, err := policy.Set(skill.PolicyPath, desired == model.StateImplicit)
	if err != nil {
		return entry, err
	}
	entry.LastManagedHash = hash
	entry.LastSyncedAt = time.Now()
	return entry, nil
}

func (a *codexAdapter) CheckRestore(entry statestore.Entry) error {
	if entry.ManagedPolicy && entry.LastManagedHash != "" {
		currentHash, err := fileutil.HashFile(entry.PolicyPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if currentHash != entry.LastManagedHash {
			return errors.New("policy file changed outside skillctl")
		}
	}
	if entry.ManagedEnabled {
		if a.client == nil || !a.enablementVerified {
			return errors.New("verified Codex app-server state is required to restore skill enablement")
		}
	}
	return nil
}

func (a *codexAdapter) Restore(entry statestore.Entry) error {
	if err := a.CheckRestore(entry); err != nil {
		return err
	}
	if entry.ManagedEnabled {
		if err := a.client.SetEnabled(entry.SkillPath, entry.OriginalEnabled); err != nil {
			return err
		}
	}
	if entry.ManagedPolicy {
		_, err := policy.Restore(entry.PolicyPath, policy.Snapshot{
			FileExisted: entry.PolicyFileExisted,
			Present:     entry.OriginalPolicyPresent,
			Value:       entry.OriginalPolicyValue,
		})
		return err
	}
	return nil
}

func (a *codexAdapter) Delete(skill model.Skill) error {
	roots, err := codex.SupportedRoots(a.cwd)
	if err != nil {
		return err
	}
	return skillfs.Delete(skill, roots)
}

func (a *codexAdapter) Close() error {
	if a.client == nil {
		return nil
	}
	err := a.client.Close()
	a.client = nil
	a.enablementVerified = false
	return err
}
