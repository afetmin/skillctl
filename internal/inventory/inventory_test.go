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
