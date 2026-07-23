package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"skillctl/internal/fileutil"
	"skillctl/internal/model"
)

const CurrentVersion = 2

type State struct {
	Version int              `json:"version"`
	Agent   model.Agent      `json:"agent"`
	Entries map[string]Entry `json:"entries"`
}

type Entry struct {
	Agent                   model.Agent `json:"agent"`
	SkillID                 string      `json:"skill_id"`
	SkillPath               string      `json:"skill_path"`
	SkillConfigName         string      `json:"skill_config_name,omitempty"`
	Scope                   model.Scope `json:"scope"`
	PolicyPath              string      `json:"policy_path"`
	ManagedPolicy           bool        `json:"managed_policy"`
	ManagedEnabled          bool        `json:"managed_enabled"`
	PolicyFileExisted       bool        `json:"policy_file_existed"`
	OriginalPolicyPresent   bool        `json:"original_policy_present"`
	OriginalPolicyValue     bool        `json:"original_policy_value"`
	OriginalEnabled         bool        `json:"original_enabled"`
	LastManagedHash         string      `json:"last_managed_hash,omitempty"`
	OverridePath            string      `json:"override_path,omitempty"`
	OriginalOverridePresent bool        `json:"original_override_present,omitempty"`
	OriginalOverrideValue   string      `json:"original_override_value,omitempty"`
	LastManagedOverride     string      `json:"last_managed_override,omitempty"`
	LastSyncedAt            time.Time   `json:"last_synced_at"`
}

func Default(agent model.Agent) State {
	return State{Version: CurrentVersion, Agent: agent, Entries: map[string]Entry{}}
}

func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "skillctl"), nil
}

func Path(dir string, agent model.Agent) string {
	return filepath.Join(dir, string(agent)+".json")
}

func RuntimePath(dir string) string {
	return filepath.Join(dir, "runtime.json")
}

func LoadOrDefault(path string, agent model.Agent) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(agent), nil
	}
	if err != nil {
		return State{}, err
	}
	var value State
	if err := json.Unmarshal(data, &value); err != nil {
		return State{}, fmt.Errorf("parse state: %w", err)
	}
	if value.Version != CurrentVersion {
		return State{}, fmt.Errorf("unsupported state version %d in %s; restore the backup or manually convert it to version %d", value.Version, path, CurrentVersion)
	}
	if value.Agent != agent {
		return State{}, fmt.Errorf("state journal %s belongs to %q, not %q", path, value.Agent, agent)
	}
	if value.Entries == nil {
		value.Entries = map[string]Entry{}
	}
	for key, entry := range value.Entries {
		if entry.Agent != agent {
			return State{}, fmt.Errorf("state entry %q belongs to %q, not %q", key, entry.Agent, agent)
		}
		if entry.Scope != model.ScopeUser && entry.Scope != model.ScopeRepo {
			return State{}, fmt.Errorf("state entry %q has unsupported scope %q", key, entry.Scope)
		}
		if !supportedEntry(entry) {
			return State{}, fmt.Errorf("state entry %q has unsupported Skill ID %q; manually convert the journal to the final personal/project format", key, entry.SkillID)
		}
	}
	return value, nil
}

func supportedEntry(entry Entry) bool {
	if entry.Scope == model.ScopeRepo {
		return strings.HasPrefix(entry.SkillID, string(entry.Agent)+":repo:")
	}
	switch entry.Agent {
	case model.AgentCodex:
		return strings.HasPrefix(entry.SkillID, "codex:user:agents:") ||
			strings.HasPrefix(entry.SkillID, "codex:user:codex:")
	case model.AgentClaude:
		return strings.HasPrefix(entry.SkillID, "claude:user:claude:")
	default:
		return false
	}
}

func Save(path string, value State) error {
	if !value.Agent.Valid() {
		return fmt.Errorf("invalid state journal agent %q", value.Agent)
	}
	value.Version = CurrentVersion
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fileutil.WriteAtomic(path, data, 0o600)
}

type Runtime struct {
	Version      int         `json:"version"`
	WatcherAgent model.Agent `json:"watcher_agent,omitempty"`
}

func LoadRuntime(path string) (Runtime, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Runtime{Version: CurrentVersion}, nil
	}
	if err != nil {
		return Runtime{}, err
	}
	var value Runtime
	if err := json.Unmarshal(data, &value); err != nil {
		return Runtime{}, fmt.Errorf("parse runtime state: %w", err)
	}
	if value.Version != CurrentVersion {
		return Runtime{}, fmt.Errorf("unsupported runtime state version %d", value.Version)
	}
	if value.WatcherAgent != "" && !value.WatcherAgent.Valid() {
		return Runtime{}, fmt.Errorf("invalid watcher agent %q", value.WatcherAgent)
	}
	return value, nil
}

func SaveRuntime(path string, value Runtime) error {
	value.Version = CurrentVersion
	if value.WatcherAgent != "" && !value.WatcherAgent.Valid() {
		return fmt.Errorf("invalid watcher agent %q", value.WatcherAgent)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteAtomic(path, append(data, '\n'), 0o600)
}
