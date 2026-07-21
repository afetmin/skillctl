package tui

import (
	"testing"

	"skillctl/internal/model"
	"skillctl/internal/service"
)

func TestPresentationFor(t *testing.T) {
	tests := []struct {
		name        string
		skill       service.SkillStatus
		pending     *pendingChange
		wantDesired model.InvocationState
		wantStatus  string
		wantMarker  string
	}{
		{
			name: "已同步",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateManual,
				Desired: model.StateManual,
			},
			wantDesired: model.StateManual,
			wantStatus:  "synced",
			wantMarker:  "◆",
		},
		{
			name: "存在漂移",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateManual,
				Desired: model.StateImplicit,
			},
			wantDesired: model.StateImplicit,
			wantStatus:  "drift",
			wantMarker:  "!",
		},
		{
			name: "已暂存修改",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateManual,
				Desired: model.StateManual,
			},
			pending: &pendingChange{
				Desired:     model.StateDisabled,
				BaseActual:  model.StateManual,
				BaseDesired: model.StateManual,
			},
			wantDesired: model.StateDisabled,
			wantStatus:  "pending",
			wantMarker:  "~",
		},
		{
			name: "暂存修改冲突",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateManual,
				Desired: model.StateManual,
			},
			pending: &pendingChange{
				Desired:     model.StateImplicit,
				BaseActual:  model.StateManual,
				BaseDesired: model.StateManual,
				Conflict:    true,
			},
			wantDesired: model.StateImplicit,
			wantStatus:  "conflict",
			wantMarker:  "×",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := uiModel{pending: map[string]pendingChange{}}
			if test.pending != nil {
				m.pending[test.skill.ID] = *test.pending
			}

			got := m.presentationFor(test.skill)
			if got.Desired != test.wantDesired {
				t.Fatalf("Desired = %q, want %q", got.Desired, test.wantDesired)
			}
			if got.Status != test.wantStatus {
				t.Fatalf("Status = %q, want %q", got.Status, test.wantStatus)
			}
			if got.Marker != test.wantMarker {
				t.Fatalf("Marker = %q, want %q", got.Marker, test.wantMarker)
			}
		})
	}
}
