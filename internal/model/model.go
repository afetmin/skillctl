package model

import "time"

type Agent string

const (
	AgentCodex  Agent = "codex"
	AgentClaude Agent = "claude"
)

func (a Agent) Valid() bool {
	return a == AgentCodex || a == AgentClaude
}

type InvocationState string

const (
	StateImplicit InvocationState = "implicit"
	StateNameOnly InvocationState = "name-only"
	StateManual   InvocationState = "manual"
	StateDisabled InvocationState = "disabled"
)

func (s InvocationState) Valid() bool {
	switch s {
	case StateImplicit, StateNameOnly, StateManual, StateDisabled:
		return true
	default:
		return false
	}
}

func ValidState(agent Agent, state InvocationState) bool {
	if !state.Valid() {
		return false
	}
	return agent == AgentClaude || state != StateNameOnly
}

func States(agent Agent) []InvocationState {
	states := []InvocationState{StateImplicit}
	if agent == AgentClaude {
		states = append(states, StateNameOnly)
	}
	return append(states, StateManual, StateDisabled)
}

type Scope string

const (
	ScopeUser Scope = "user"
	ScopeRepo Scope = "repo"
)

type DiscoveryStatus string

const (
	DiscoveryComplete           DiscoveryStatus = "complete"
	DiscoveryPartialUnsupported DiscoveryStatus = "partial_unsupported"
	DiscoveryPartialFailure     DiscoveryStatus = "partial_failure"
)

type DiscoveryWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DiscoveryReport struct {
	Status   DiscoveryStatus    `json:"status"`
	Warnings []DiscoveryWarning `json:"warnings,omitempty"`
}

func (r DiscoveryReport) Complete() bool {
	return r.Status == DiscoveryComplete
}

type Skill struct {
	Agent       Agent           `json:"agent"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Path        string          `json:"path"`
	Scope       Scope           `json:"scope"`
	Source      string          `json:"source"`
	Enabled     bool            `json:"enabled"`
	Policy      *bool           `json:"allow_implicit_invocation,omitempty"`
	PolicyPath  string          `json:"policy_path"`
	Aliases     []string        `json:"-"`
	NativeState InvocationState `json:"-"`
	ReadOnly    bool            `json:"read_only,omitempty"`
	Shadowed    bool            `json:"shadowed,omitempty"`
	BlockedBy   string          `json:"blocked_by,omitempty"`
}

func (s Skill) ActualState() InvocationState {
	if s.NativeState.Valid() {
		return s.NativeState
	}
	if !s.Enabled {
		return StateDisabled
	}
	if s.Policy != nil && !*s.Policy {
		return StateManual
	}
	return StateImplicit
}

func (s Skill) ManagedByDefault() bool {
	return s.Scope == ScopeUser && !s.ReadOnly
}

type Change struct {
	Agent   Agent           `json:"agent"`
	SkillID string          `json:"skill_id"`
	Name    string          `json:"name"`
	Path    string          `json:"path"`
	From    InvocationState `json:"from"`
	To      InvocationState `json:"to"`
	Applied bool            `json:"applied"`
	Reason  string          `json:"reason,omitempty"`
}

type SyncReport struct {
	Agent             Agent              `json:"agent"`
	Scanned           int                `json:"scanned"`
	Managed           int                `json:"managed"`
	Changed           int                `json:"changed"`
	Skipped           int                `json:"skipped"`
	Conflicts         int                `json:"conflicts"`
	DryRun            bool               `json:"dry_run"`
	Changes           []Change           `json:"changes"`
	AppliedChanges    []Change           `json:"applied_changes,omitempty"`
	SkippedChanges    []Change           `json:"skipped_changes,omitempty"`
	ConflictedChanges []Change           `json:"conflicted_changes,omitempty"`
	Warnings          []DiscoveryWarning `json:"warnings,omitempty"`
	Discovery         DiscoveryReport    `json:"discovery"`
	Orphans           []OrphanRecord     `json:"orphans,omitempty"`
	At                time.Time          `json:"at"`
}

type OrphanRecord struct {
	Agent     Agent           `json:"agent"`
	Kind      string          `json:"kind"`
	Selector  string          `json:"selector,omitempty"`
	SkillID   string          `json:"skill_id,omitempty"`
	SkillPath string          `json:"skill_path,omitempty"`
	Desired   InvocationState `json:"desired,omitempty"`
}
