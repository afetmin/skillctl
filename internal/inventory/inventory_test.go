package inventory

import (
	"testing"

	"skillctl/internal/model"
	"skillctl/internal/service"
)

func TestApplySearchesDescription(t *testing.T) {
	items := []service.SkillStatus{
		{Skill: model.Skill{ID: "skill-a", Name: "alpha", Description: "Next.js App Router guidance"}},
		{Skill: model.Skill{ID: "skill-b", Name: "beta", Description: "Database migration helper"}},
	}

	got := Apply(items, Filter{Query: "app router"})

	if len(got) != 1 || got[0].ID != "skill-a" {
		t.Fatalf("Apply() = %#v, want skill-a", got)
	}
}

func TestApplySearchDoesNotMatchSource(t *testing.T) {
	items := []service.SkillStatus{
		{Skill: model.Skill{ID: "skill-a", Name: "alpha", Source: "vercel"}},
	}

	got := Apply(items, Filter{Query: "vercel"})

	if len(got) != 0 {
		t.Fatalf("Apply() = %#v, want no skills", got)
	}
}

func TestApplyFiltersBySourceOptionKey(t *testing.T) {
	items := []service.SkillStatus{
		{Skill: model.Skill{ID: "codex-skill", Name: "codex", Scope: model.ScopeUser, Source: "codex"}},
		{Skill: model.Skill{ID: "agent-skill", Name: "agent", Scope: model.ScopeUser, Source: "agents"}},
	}

	got := Apply(items, Filter{SourceKey: "group:personal:codex"})

	if len(got) != 1 || got[0].ID != "codex-skill" {
		t.Fatalf("Apply() = %#v, want codex-skill", got)
	}
}

func TestApplyFiltersByActualState(t *testing.T) {
	items := []service.SkillStatus{
		{Skill: model.Skill{ID: "drifting"}, Actual: model.StateManual, Desired: model.StateDisabled},
		{Skill: model.Skill{ID: "disabled"}, Actual: model.StateDisabled, Desired: model.StateDisabled},
	}

	got := Apply(items, Filter{State: model.StateManual})

	if len(got) != 1 || got[0].ID != "drifting" {
		t.Fatalf("Apply() = %#v, want drifting skill in its actual manual state", got)
	}
}

func TestApplyCombinesSearchStateAndSource(t *testing.T) {
	items := []service.SkillStatus{
		{Skill: model.Skill{ID: "matching", Name: "needle", Scope: model.ScopeUser, Source: "codex"}, Actual: model.StateManual},
		{Skill: model.Skill{ID: "wrong-query", Name: "other", Scope: model.ScopeUser, Source: "codex"}, Actual: model.StateManual},
		{Skill: model.Skill{ID: "wrong-state", Name: "needle", Scope: model.ScopeUser, Source: "codex"}, Actual: model.StateImplicit},
		{Skill: model.Skill{ID: "wrong-source", Name: "needle", Scope: model.ScopeUser, Source: "agents"}, Actual: model.StateManual},
	}

	got := Apply(items, Filter{Query: "needle", State: model.StateManual, SourceKey: "group:personal:codex"})

	if len(got) != 1 || got[0].ID != "matching" {
		t.Fatalf("Apply() = %#v, want only matching skill", got)
	}
}

func TestSourceOptionsUseOtherFiltersAndKeepZeroCounts(t *testing.T) {
	items := []service.SkillStatus{
		{Skill: model.Skill{ID: "codex", Name: "match", Scope: model.ScopeUser, Source: "codex"}, Actual: model.StateImplicit},
		{Skill: model.Skill{ID: "claude", Name: "match", Scope: model.ScopeUser, Source: "claude"}, Actual: model.StateManual},
		{Skill: model.Skill{ID: "agents", Name: "other", Scope: model.ScopeUser, Source: "agents"}, Actual: model.StateManual},
	}

	got := SourceOptions(items, Filter{Query: "match", State: model.StateManual, SourceKey: "group:personal:codex"})

	want := map[string]int{
		"all":                   1,
		"group:personal:codex":  0,
		"group:personal:claude": 1,
		"group:personal:agents": 0,
		"category:personal":     1,
	}
	for key, count := range want {
		if option, ok := sourceOptionByKey(got, key); !ok || option.Count != count {
			t.Fatalf("SourceOptions()[%q] = %#v, want count %d", key, option, count)
		}
	}
}

func TestStateOptionsUseOtherFiltersAndIgnoreSelectedState(t *testing.T) {
	items := []service.SkillStatus{
		{Skill: model.Skill{ID: "implicit", Name: "match", Scope: model.ScopeUser, Source: "codex"}, Actual: model.StateImplicit},
		{Skill: model.Skill{ID: "manual", Name: "match", Scope: model.ScopeUser, Source: "claude"}, Actual: model.StateManual},
		{Skill: model.Skill{ID: "disabled", Name: "other", Scope: model.ScopeUser, Source: "codex"}, Actual: model.StateDisabled},
	}

	got := StateOptions(items, Filter{Query: "match", State: model.StateManual, SourceKey: "group:personal:codex"}, model.States(model.AgentCodex))

	want := map[model.InvocationState]int{
		"":                  1,
		model.StateImplicit: 1,
		model.StateManual:   0,
		model.StateDisabled: 0,
	}
	for _, option := range got {
		if option.Count != want[option.State] {
			t.Fatalf("StateOptions()[%q] = %d, want %d", option.State, option.Count, want[option.State])
		}
		delete(want, option.State)
	}
	if len(want) != 0 {
		t.Fatalf("StateOptions() missing states: %#v", want)
	}
}

func sourceOptionByKey(options []SourceOption, key string) (SourceOption, bool) {
	for _, option := range options {
		if option.Key == key {
			return option, true
		}
	}
	return SourceOption{}, false
}
