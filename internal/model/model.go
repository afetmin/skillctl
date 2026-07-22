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

type DiscoveryStatus string

const (
	DiscoveryComplete           DiscoveryStatus = "complete"
	DiscoveryPartialUnsupported DiscoveryStatus = "partial_unsupported"
	DiscoveryPartialFailure     DiscoveryStatus = "partial_failure"
)

type DiscoveryWarning struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	PluginID string `json:"plugin_id,omitempty"`
}

type DiscoveryReport struct {
	Status   DiscoveryStatus    `json:"status"`
	Warnings []DiscoveryWarning `json:"warnings,omitempty"`
}

func (r DiscoveryReport) Complete() bool {
	return r.Status == DiscoveryComplete
}

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
	Scanned   int                `json:"scanned"`
	Managed   int                `json:"managed"`
	Changed   int                `json:"changed"`
	Skipped   int                `json:"skipped"`
	Conflicts int                `json:"conflicts"`
	DryRun    bool               `json:"dry_run"`
	Changes   []Change           `json:"changes"`
	Warnings  []DiscoveryWarning `json:"warnings,omitempty"`
	Discovery DiscoveryReport    `json:"discovery"`
	Orphans   []OrphanRecord     `json:"orphans,omitempty"`
	At        time.Time          `json:"at"`
}

type OrphanRecord struct {
	Kind      string          `json:"kind"`
	Selector  string          `json:"selector,omitempty"`
	SkillID   string          `json:"skill_id,omitempty"`
	SkillPath string          `json:"skill_path,omitempty"`
	Desired   InvocationState `json:"desired,omitempty"`
}
