package service

import (
	"testing"

	"skillctl/internal/model"
)

func TestMergeSkillsMarksPluginMissingFromAppServerDisabled(t *testing.T) {
	pluginFallback := model.Skill{
		ID:      "codex:plugin:openai-bundled:sites:sites-building",
		Name:    "sites-building",
		Path:    "/plugins/cache/openai-bundled/sites/skills/sites-building/SKILL.md",
		Scope:   model.ScopePlugin,
		Enabled: true,
	}

	got := mergeSkills(nil, []model.Skill{pluginFallback})

	if len(got) != 1 {
		t.Fatalf("mergeSkills() returned %d skills, want 1", len(got))
	}
	if got[0].Enabled {
		t.Fatalf("mergeSkills() plugin fallback enabled = true, want false when app-server omitted it")
	}
}

func TestMergeSkillsKeepsNonPluginFilesystemSkillEnabled(t *testing.T) {
	userFallback := model.Skill{
		ID:      "codex:user:agents:custom",
		Name:    "custom",
		Path:    "/users/skills/custom/SKILL.md",
		Scope:   model.ScopeUser,
		Enabled: true,
	}

	got := mergeSkills(nil, []model.Skill{userFallback})

	if len(got) != 1 {
		t.Fatalf("mergeSkills() returned %d skills, want 1", len(got))
	}
	if !got[0].Enabled {
		t.Fatalf("mergeSkills() user fallback enabled = false, want true")
	}
}
