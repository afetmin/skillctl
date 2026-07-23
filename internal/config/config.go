package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"skillctl/internal/fileutil"
	"skillctl/internal/model"
)

const CurrentVersion = 2

type Error struct {
	Err error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type Config struct {
	Version int    `yaml:"version" json:"version"`
	Agents  Agents `yaml:"agents" json:"agents"`
}

type Defaults struct {
	Invocation model.InvocationState `yaml:"invocation" json:"invocation"`
}

type Profile struct {
	Implicit []string `yaml:"implicit" json:"implicit"`
	NameOnly []string `yaml:"name_only,omitempty" json:"name_only,omitempty"`
	Manual   []string `yaml:"manual,omitempty" json:"manual,omitempty"`
	Disabled []string `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

type Agents struct {
	Codex  AgentConfig `yaml:"codex" json:"codex"`
	Claude AgentConfig `yaml:"claude" json:"claude"`
}

type AgentConfig struct {
	Command       string             `yaml:"command" json:"command"`
	ActiveProfile string             `yaml:"active_profile" json:"active_profile"`
	Defaults      Defaults           `yaml:"defaults" json:"defaults"`
	Profiles      map[string]Profile `yaml:"profiles" json:"profiles"`
}

func Default() Config {
	defaultAgent := func(command string) AgentConfig {
		return AgentConfig{
			Command:       command,
			ActiveProfile: "default",
			Defaults:      Defaults{Invocation: model.StateManual},
			Profiles: map[string]Profile{
				"default": {Implicit: []string{}, NameOnly: []string{}, Manual: []string{}, Disabled: []string{}},
			},
		}
	}
	return Config{
		Version: CurrentVersion,
		Agents: Agents{
			Codex:  defaultAgent("codex"),
			Claude: defaultAgent("claude"),
		},
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
	var header struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return Config{}, &Error{Err: fmt.Errorf("parse config: %w", err)}
	}
	if header.Version != CurrentVersion {
		return Config{}, &Error{Err: fmt.Errorf("unsupported config version %d; back up the file and manually convert it to version %d", header.Version, CurrentVersion)}
	}
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
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
		return fmt.Errorf("unsupported config version %d; back up the file and manually convert it to version %d", c.Version, CurrentVersion)
	}
	if err := validateAgent(model.AgentCodex, c.Agents.Codex); err != nil {
		return err
	}
	return validateAgent(model.AgentClaude, c.Agents.Claude)
}

func validateAgent(agent model.Agent, value AgentConfig) error {
	if value.Command == "" {
		return fmt.Errorf("%s command is required", agent)
	}
	if value.ActiveProfile == "" {
		return errors.New("active_profile is required")
	}
	if !model.ValidState(agent, value.Defaults.Invocation) {
		return fmt.Errorf("invalid %s default invocation state %q", agent, value.Defaults.Invocation)
	}
	if _, ok := value.Profiles[value.ActiveProfile]; !ok {
		return fmt.Errorf("%s active profile %q does not exist", agent, value.ActiveProfile)
	}
	for name, profile := range value.Profiles {
		if agent == model.AgentCodex && len(profile.NameOnly) > 0 {
			return fmt.Errorf("codex profile %q contains name_only selectors, but Codex does not support name-only", name)
		}
		seen := map[string]model.InvocationState{}
		groups := []struct {
			state     model.InvocationState
			selectors []string
		}{
			{state: model.StateImplicit, selectors: profile.Implicit},
			{state: model.StateNameOnly, selectors: profile.NameOnly},
			{state: model.StateManual, selectors: profile.Manual},
			{state: model.StateDisabled, selectors: profile.Disabled},
		}
		for _, group := range groups {
			for _, selector := range group.selectors {
				if !supportedSelector(agent, selector) {
					return fmt.Errorf("%s profile %q contains unsupported selector %q", agent, name, selector)
				}
				if previous, ok := seen[selector]; ok {
					return fmt.Errorf("%s profile %q contains %q in both %s and %s", agent, name, selector, previous, group.state)
				}
				seen[selector] = group.state
			}
		}
	}
	return nil
}

func supportedSelector(agent model.Agent, selector string) bool {
	switch {
	case strings.HasPrefix(selector, "codex:"):
		return agent == model.AgentCodex &&
			(strings.HasPrefix(selector, "codex:user:agents:") ||
				strings.HasPrefix(selector, "codex:user:codex:") ||
				strings.HasPrefix(selector, "codex:repo:"))
	case strings.HasPrefix(selector, "claude:"):
		return agent == model.AgentClaude &&
			(strings.HasPrefix(selector, "claude:user:claude:") ||
				strings.HasPrefix(selector, "claude:repo:"))
	default:
		return true
	}
}

func (c Config) Agent(agent model.Agent) (AgentConfig, error) {
	switch agent {
	case model.AgentCodex:
		return c.Agents.Codex, nil
	case model.AgentClaude:
		return c.Agents.Claude, nil
	default:
		return AgentConfig{}, fmt.Errorf("unsupported agent %q", agent)
	}
}

func (c Config) Active(agent model.Agent) Profile {
	value, _ := c.Agent(agent)
	return value.Profiles[value.ActiveProfile]
}

func (c *Config) SetState(agent model.Agent, id, name string, desired model.InvocationState, aliases ...string) error {
	if !model.ValidState(agent, desired) {
		return fmt.Errorf("%s does not support invocation state %q", agent, desired)
	}
	value, err := c.Agent(agent)
	if err != nil {
		return err
	}
	profile := value.Profiles[value.ActiveProfile]
	for _, selector := range append([]string{id, name}, aliases...) {
		if selector == "" {
			continue
		}
		profile.Implicit = remove(profile.Implicit, selector)
		profile.NameOnly = remove(profile.NameOnly, selector)
		profile.Manual = remove(profile.Manual, selector)
		profile.Disabled = remove(profile.Disabled, selector)
	}
	switch desired {
	case model.StateImplicit:
		profile.Implicit = append(profile.Implicit, id)
	case model.StateNameOnly:
		profile.NameOnly = append(profile.NameOnly, id)
	case model.StateManual:
		profile.Manual = append(profile.Manual, id)
	case model.StateDisabled:
		profile.Disabled = append(profile.Disabled, id)
	}
	sort.Strings(profile.Implicit)
	sort.Strings(profile.NameOnly)
	sort.Strings(profile.Manual)
	sort.Strings(profile.Disabled)
	value.Profiles[value.ActiveProfile] = profile
	if agent == model.AgentCodex {
		c.Agents.Codex = value
	} else {
		c.Agents.Claude = value
	}
	return nil
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
