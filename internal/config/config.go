package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"skillctl/internal/fileutil"
	"skillctl/internal/model"
)

const CurrentVersion = 1

type Error struct {
	Err error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type Config struct {
	Version       int                `yaml:"version" json:"version"`
	ActiveProfile string             `yaml:"active_profile" json:"active_profile"`
	Defaults      Defaults           `yaml:"defaults" json:"defaults"`
	Profiles      map[string]Profile `yaml:"profiles" json:"profiles"`
	Adapters      Adapters           `yaml:"adapters" json:"adapters"`
}

type Defaults struct {
	Invocation model.InvocationState `yaml:"invocation" json:"invocation"`
}

type Profile struct {
	Implicit []string `yaml:"implicit" json:"implicit"`
	Disabled []string `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

type Adapters struct {
	Codex CodexAdapter `yaml:"codex" json:"codex"`
}

type CodexAdapter struct {
	Command string `yaml:"command" json:"command"`
}

func Default() Config {
	return Config{
		Version:       CurrentVersion,
		ActiveProfile: "default",
		Defaults:      Defaults{Invocation: model.StateManual},
		Profiles: map[string]Profile{
			"default": {Implicit: []string{}, Disabled: []string{}},
		},
		Adapters: Adapters{Codex: CodexAdapter{Command: "codex"}},
	}
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "skillctl", "config.yaml"), nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, &Error{Err: err}
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, &Error{Err: fmt.Errorf("parse config: %w", err)}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, &Error{Err: err}
	}
	return cfg, nil
}

func LoadOrDefault(path string) (Config, bool, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Default(), false, nil
	}
	return Config{}, false, err
}

func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return fileutil.WriteAtomic(path, data, 0o600)
}

func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.ActiveProfile == "" {
		return errors.New("active_profile is required")
	}
	if !c.Defaults.Invocation.Valid() {
		return fmt.Errorf("invalid default invocation state %q", c.Defaults.Invocation)
	}
	if c.Defaults.Invocation != model.StateManual {
		return errors.New("v1 requires defaults.invocation to be manual")
	}
	if _, ok := c.Profiles[c.ActiveProfile]; !ok {
		return fmt.Errorf("active profile %q does not exist", c.ActiveProfile)
	}
	for name, profile := range c.Profiles {
		disabled := map[string]bool{}
		for _, selector := range profile.Disabled {
			disabled[selector] = true
		}
		for _, selector := range profile.Implicit {
			if disabled[selector] {
				return fmt.Errorf("profile %q contains %q in both implicit and disabled", name, selector)
			}
		}
	}
	return nil
}

func (c Config) Active() Profile {
	return c.Profiles[c.ActiveProfile]
}

func (c *Config) SetState(id, name string, desired model.InvocationState, aliases ...string) {
	profile := c.Profiles[c.ActiveProfile]
	for _, selector := range append([]string{id, name}, aliases...) {
		if selector == "" {
			continue
		}
		profile.Implicit = remove(profile.Implicit, selector)
		profile.Disabled = remove(profile.Disabled, selector)
	}
	switch desired {
	case model.StateImplicit:
		profile.Implicit = append(profile.Implicit, id)
	case model.StateDisabled:
		profile.Disabled = append(profile.Disabled, id)
	}
	sort.Strings(profile.Implicit)
	sort.Strings(profile.Disabled)
	c.Profiles[c.ActiveProfile] = profile
}

func remove(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
