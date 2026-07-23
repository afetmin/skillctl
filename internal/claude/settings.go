package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"skillctl/internal/fileutil"
	"skillctl/internal/model"
)

const (
	OverrideOn                = "on"
	OverrideNameOnly          = "name-only"
	OverrideUserInvocableOnly = "user-invocable-only"
	OverrideOff               = "off"
)

type SettingsPaths struct {
	User    string
	Shared  string
	Local   string
	Managed []string
}

type EffectiveOverride struct {
	Value   string
	Source  string
	Path    string
	Present bool
}

func Paths(cwd string) (SettingsPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return SettingsPaths{}, err
	}
	root := repositoryRoot(cwd)
	paths := SettingsPaths{
		User:   filepath.Join(home, ".claude", "settings.json"),
		Shared: filepath.Join(root, ".claude", "settings.json"),
		Local:  filepath.Join(root, ".claude", "settings.local.json"),
	}
	if runtime.GOOS == "darwin" {
		paths.Managed = []string{"/Library/Application Support/ClaudeCode/managed-settings.json"}
	} else {
		paths.Managed = []string{"/etc/claude-code/managed-settings.json"}
	}
	return paths, nil
}

func Effective(paths SettingsPaths, name string) (EffectiveOverride, error) {
	layers := []struct {
		source string
		path   string
	}{
		{source: "project-local", path: paths.Local},
		{source: "project-shared", path: paths.Shared},
		{source: "user", path: paths.User},
	}
	for _, layer := range layers {
		value, present, err := ReadOverride(layer.path, name)
		if err != nil {
			return EffectiveOverride{}, err
		}
		if present {
			return EffectiveOverride{Value: value, Source: layer.source, Path: layer.path, Present: true}, nil
		}
	}
	return EffectiveOverride{Value: OverrideOn, Source: "default"}, nil
}

func ReadOverride(path, name string) (string, bool, error) {
	root, err := readRoot(path)
	if err != nil {
		return "", false, err
	}
	raw, ok := root["skillOverrides"]
	if !ok {
		return "", false, nil
	}
	var overrides map[string]string
	if err := json.Unmarshal(raw, &overrides); err != nil {
		return "", false, fmt.Errorf("parse %s skillOverrides: %w", path, err)
	}
	value, ok := overrides[name]
	if !ok {
		return "", false, nil
	}
	if !validOverride(value) {
		return "", false, fmt.Errorf("invalid Claude skill override %q for %s in %s", value, name, path)
	}
	return value, true, nil
}

func WriteOverride(path, name string, value *string) error {
	root, err := readRoot(path)
	if err != nil {
		return err
	}
	overrides := map[string]string{}
	if raw, ok := root["skillOverrides"]; ok {
		if err := json.Unmarshal(raw, &overrides); err != nil {
			return fmt.Errorf("parse %s skillOverrides: %w", path, err)
		}
	}
	if value == nil {
		delete(overrides, name)
	} else {
		if !validOverride(*value) {
			return fmt.Errorf("invalid Claude skill override %q", *value)
		}
		overrides[name] = *value
	}
	if len(overrides) == 0 {
		delete(root, "skillOverrides")
	} else {
		raw, err := json.Marshal(overrides)
		if err != nil {
			return err
		}
		root["skillOverrides"] = raw
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteAtomic(path, append(data, '\n'), 0o600)
}

func OverrideForState(state model.InvocationState) (string, error) {
	switch state {
	case model.StateImplicit:
		return OverrideOn, nil
	case model.StateNameOnly:
		return OverrideNameOnly, nil
	case model.StateManual:
		return OverrideUserInvocableOnly, nil
	case model.StateDisabled:
		return OverrideOff, nil
	default:
		return "", fmt.Errorf("unsupported Claude invocation state %q", state)
	}
}

func StateForOverride(value string) (model.InvocationState, error) {
	switch value {
	case OverrideOn:
		return model.StateImplicit, nil
	case OverrideNameOnly:
		return model.StateNameOnly, nil
	case OverrideUserInvocableOnly:
		return model.StateManual, nil
	case OverrideOff:
		return model.StateDisabled, nil
	default:
		return "", fmt.Errorf("invalid Claude skill override %q", value)
	}
}

func ManagedSettingsPresent(paths SettingsPaths) bool {
	for _, path := range paths.Managed {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func readRoot(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse Claude settings %s: %w", path, err)
	}
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	return root, nil
}

func validOverride(value string) bool {
	switch value {
	case OverrideOn, OverrideNameOnly, OverrideUserInvocableOnly, OverrideOff:
		return true
	default:
		return false
	}
}
