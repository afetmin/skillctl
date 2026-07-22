package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"skillctl/internal/fileutil"
)

const CurrentVersion = 1

type State struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

type Entry struct {
	SkillID               string    `json:"skill_id"`
	SkillPath             string    `json:"skill_path"`
	SkillConfigName       string    `json:"skill_config_name,omitempty"`
	PolicyPath            string    `json:"policy_path"`
	ManagedPolicy         bool      `json:"managed_policy"`
	ManagedEnabled        bool      `json:"managed_enabled"`
	PolicyFileExisted     bool      `json:"policy_file_existed"`
	OriginalPolicyPresent bool      `json:"original_policy_present"`
	OriginalPolicyValue   bool      `json:"original_policy_value"`
	OriginalEnabled       bool      `json:"original_enabled"`
	LastManagedHash       string    `json:"last_managed_hash,omitempty"`
	LastSyncedAt          time.Time `json:"last_synced_at"`
}

func Default() State {
	return State{Version: CurrentVersion, Entries: map[string]Entry{}}
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "skillctl", "state.json"), nil
}

func LoadOrDefault(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return State{}, err
	}
	var value State
	if err := json.Unmarshal(data, &value); err != nil {
		return State{}, fmt.Errorf("parse state: %w", err)
	}
	if value.Version != CurrentVersion {
		return State{}, fmt.Errorf("unsupported state version %d", value.Version)
	}
	if value.Entries == nil {
		value.Entries = map[string]Entry{}
	}
	return value, nil
}

func Save(path string, value State) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fileutil.WriteAtomic(path, data, 0o600)
}
