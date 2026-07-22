package model

import "time"

type InvocationState string

const (
	StateImplicit InvocationState = "implicit"
	StateManual   InvocationState = "manual"
	StateDisabled InvocationState = "disabled"
)

func (s InvocationState) Valid() bool {
	switch s {
	case StateImplicit, StateManual, StateDisabled:
		return true
	default:
		return false
	}
}

type Scope string

const (
	ScopeUser   Scope = "user"
	ScopePlugin Scope = "plugin"
	ScopeRepo   Scope = "repo"
	ScopeSystem Scope = "system"
	ScopeAdmin  Scope = "admin"
	ScopeOther  Scope = "other"
)

type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Path        string   `json:"path"`
	Scope       Scope    `json:"scope"`
	Source      string   `json:"source"`
	Enabled     bool     `json:"enabled"`
	Policy      *bool    `json:"allow_implicit_invocation,omitempty"`
	PolicyPath  string   `json:"policy_path"`
	ConfigName  string   `json:"-"`
	PluginID    string   `json:"plugin_id,omitempty"`
	Aliases     []string `json:"-"`
}

func (s Skill) ActualState() InvocationState {
	if !s.Enabled {
		return StateDisabled
	}
	if s.Policy != nil && !*s.Policy {
		return StateManual
	}
	return StateImplicit
}

func (s Skill) ManagedByDefault() bool {
	return s.Scope == ScopeUser || s.Scope == ScopePlugin
}

type Change struct {
	SkillID string          `json:"skill_id"`
	Name    string          `json:"name"`
	Path    string          `json:"path"`
	From    InvocationState `json:"from"`
	To      InvocationState `json:"to"`
	Applied bool            `json:"applied"`
	Message string          `json:"message,omitempty"`
}

type SyncReport struct {
	Scanned   int       `json:"scanned"`
	Managed   int       `json:"managed"`
	Changed   int       `json:"changed"`
	Skipped   int       `json:"skipped"`
	Conflicts int       `json:"conflicts"`
	DryRun    bool      `json:"dry_run"`
	Changes   []Change  `json:"changes"`
	Warnings  []string  `json:"warnings,omitempty"`
	At        time.Time `json:"at"`
}
