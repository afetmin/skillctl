package tui

import (
	"testing"

	"skillctl/internal/model"
	"skillctl/internal/service"
)

func TestPresentationFor(t *testing.T) {
	tests := []struct {
		name          string
		skill         service.SkillStatus
		pending       *pendingChange
		applied       bool
		wantTarget    model.InvocationState
		wantCondition skillCondition
		wantMarker    string
		wantReadOnly  bool
	}{
		{
			name: "已同步",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateManual,
				Desired: model.StateManual,
				Managed: true,
			},
			wantCondition: conditionSynced,
			wantMarker:    "◆",
		},
		{
			name: "存在漂移",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateManual,
				Desired: model.StateImplicit,
				Managed: true,
			},
			wantCondition: conditionDrift,
			wantMarker:    "!",
		},
		{
			name: "已暂存修改",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateManual,
				Desired: model.StateManual,
				Managed: true,
			},
			pending: &pendingChange{
				Desired:     model.StateDisabled,
				BaseActual:  model.StateManual,
				BaseDesired: model.StateManual,
			},
			wantTarget:    model.StateDisabled,
			wantCondition: conditionPending,
			wantMarker:    "~",
		},
		{
			name: "暂存修改冲突",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateManual,
				Desired: model.StateManual,
				Managed: true,
			},
			pending: &pendingChange{
				Desired:     model.StateImplicit,
				BaseActual:  model.StateManual,
				BaseDesired: model.StateManual,
				Conflict:    true,
			},
			wantTarget:    model.StateImplicit,
			wantCondition: conditionConflict,
			wantMarker:    "×",
		},
		{
			name: "本次应用成功",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateImplicit,
				Desired: model.StateImplicit,
				Managed: true,
			},
			applied:       true,
			wantCondition: conditionApplied,
			wantMarker:    "✓",
		},
		{
			name: "只读优先于漂移和成功",
			skill: service.SkillStatus{
				Skill:   model.Skill{ID: "skill-a"},
				Actual:  model.StateManual,
				Desired: model.StateImplicit,
				Managed: false,
			},
			applied:       true,
			wantCondition: conditionReadOnly,
			wantMarker:    "◆",
			wantReadOnly:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := uiModel{
				pending: map[string]pendingChange{},
				applied: map[string]bool{},
			}
			if test.pending != nil {
				m.pending[test.skill.ID] = *test.pending
			}
			if test.applied {
				m.applied[test.skill.ID] = true
			}

			got := m.presentationFor(test.skill)
			if got.Target != test.wantTarget {
				t.Fatalf("Target = %q, want %q", got.Target, test.wantTarget)
			}
			if got.Condition != test.wantCondition {
				t.Fatalf("Condition = %q, want %q", got.Condition, test.wantCondition)
			}
			if got.Marker != test.wantMarker {
				t.Fatalf("Marker = %q, want %q", got.Marker, test.wantMarker)
			}
			if got.ReadOnly != test.wantReadOnly {
				t.Fatalf("ReadOnly = %t, want %t", got.ReadOnly, test.wantReadOnly)
			}
		})
	}
}

func TestReconcileApplied(t *testing.T) {
	m := uiModel{applied: map[string]bool{
		"synced":  true,
		"drifted": true,
		"missing": true,
	}}

	m.reconcileApplied([]service.SkillStatus{
		{
			Skill:   model.Skill{ID: "synced"},
			Actual:  model.StateImplicit,
			Desired: model.StateImplicit,
		},
		{
			Skill:   model.Skill{ID: "drifted"},
			Actual:  model.StateManual,
			Desired: model.StateDisabled,
		},
	})

	if !m.applied["synced"] {
		t.Fatal("无漂移的成功标记被意外移除")
	}
	if m.applied["drifted"] {
		t.Fatal("重新漂移后仍保留成功标记")
	}
	if m.applied["missing"] {
		t.Fatal("Skill 不存在后仍保留成功标记")
	}
}

func TestStageCurrentClearsAppliedMarker(t *testing.T) {
	skill := service.SkillStatus{
		Skill:   model.Skill{ID: "skill-a", Name: "Skill A"},
		Actual:  model.StateManual,
		Desired: model.StateManual,
		Managed: true,
	}
	m := uiModel{
		rows:    []tableRow{{Kind: rowSkill, Skill: skill}},
		pending: map[string]pendingChange{},
		applied: map[string]bool{"skill-a": true},
	}

	m = m.stageCurrent(model.StateImplicit)

	if m.applied["skill-a"] {
		t.Fatal("重新暂存后仍保留成功标记")
	}
	if change, ok := m.pending["skill-a"]; !ok || change.Desired != model.StateImplicit {
		t.Fatalf("暂存修改不正确: %#v", change)
	}
}

func TestStageCurrentCanApplyExistingDriftTarget(t *testing.T) {
	skill := service.SkillStatus{
		Skill:   model.Skill{ID: "skill-a", Name: "Skill A"},
		Actual:  model.StateManual,
		Desired: model.StateImplicit,
		Managed: true,
	}
	m := uiModel{
		rows:    []tableRow{{Kind: rowSkill, Skill: skill}},
		pending: map[string]pendingChange{},
		applied: map[string]bool{},
	}

	m = m.stageCurrent(model.StateImplicit)

	change, ok := m.pending["skill-a"]
	if !ok {
		t.Fatal("选择现有漂移目标后没有生成待应用修改")
	}
	if change.Desired != model.StateImplicit {
		t.Fatalf("Desired = %q, want %q", change.Desired, model.StateImplicit)
	}
	presentation := m.presentationFor(skill)
	if presentation.Target != model.StateImplicit || presentation.Condition != conditionPending {
		t.Fatalf("presentation = %#v", presentation)
	}
}

func TestStageCurrentCanResolveDriftByKeepingActualState(t *testing.T) {
	skill := service.SkillStatus{
		Skill:   model.Skill{ID: "skill-a", Name: "Skill A"},
		Actual:  model.StateManual,
		Desired: model.StateImplicit,
		Managed: true,
	}
	m := uiModel{
		rows:    []tableRow{{Kind: rowSkill, Skill: skill}},
		pending: map[string]pendingChange{},
		applied: map[string]bool{},
	}

	m = m.stageCurrent(model.StateManual)

	change, ok := m.pending["skill-a"]
	if !ok || change.Desired != model.StateManual {
		t.Fatalf("保持当前状态时没有生成用于消除漂移的修改: %#v", change)
	}
}

func TestSummarizePresentations(t *testing.T) {
	m := uiModel{
		items: []service.SkillStatus{
			{Skill: model.Skill{ID: "drift"}, Actual: model.StateManual, Desired: model.StateImplicit, Managed: true},
			{Skill: model.Skill{ID: "pending"}, Actual: model.StateManual, Desired: model.StateManual, Managed: true},
			{Skill: model.Skill{ID: "conflict"}, Actual: model.StateManual, Desired: model.StateManual, Managed: true},
			{Skill: model.Skill{ID: "applied"}, Actual: model.StateDisabled, Desired: model.StateDisabled, Managed: true},
			{Skill: model.Skill{ID: "synced"}, Actual: model.StateManual, Desired: model.StateManual, Managed: true},
		},
		pending: map[string]pendingChange{
			"pending":  {Desired: model.StateImplicit},
			"conflict": {Desired: model.StateDisabled, Conflict: true},
		},
		applied: map[string]bool{"applied": true},
	}

	got := m.summarizePresentations()
	want := presentationSummary{Drift: 1, Pending: 1, Conflict: 1, Applied: 1}
	if got != want {
		t.Fatalf("summarizePresentations() = %#v, want %#v", got, want)
	}
}

func TestRecordAppliedKeepsConflicts(t *testing.T) {
	m := uiModel{
		pending: map[string]pendingChange{
			"applied":  {Desired: model.StateImplicit},
			"conflict": {Desired: model.StateDisabled, Conflict: true},
		},
	}

	conflicts := m.recordApplied()

	if conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1", conflicts)
	}
	if !m.applied["applied"] {
		t.Fatal("成功项没有记录应用标记")
	}
	if m.applied["conflict"] {
		t.Fatal("冲突项被错误记录为应用成功")
	}
	if _, ok := m.pending["applied"]; ok {
		t.Fatal("成功项仍保留在待应用列表")
	}
	if change, ok := m.pending["conflict"]; !ok || !change.Conflict {
		t.Fatalf("冲突项没有保留: %#v", change)
	}
}

func TestNormalizeDescription(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "普通描述", value: "A useful skill", want: "A useful skill"},
		{name: "压缩空白", value: "  Build\n\nreliable\t agents  ", want: "Build reliable agents"},
		{name: "空描述", value: " \n\t ", want: "No description"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeDescription(test.value); got != test.want {
				t.Fatalf("normalizeDescription() = %q, want %q", got, test.want)
			}
		})
	}
}
