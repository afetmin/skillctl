package tui

import (
	"errors"
	"testing"

	"skillctl/internal/model"
	"skillctl/internal/service"
)

func TestBeginDeleteOpensConfirmationForDeletableSkill(t *testing.T) {
	skill := service.SkillStatus{Skill: model.Skill{
		ID:    "codex:user:agents:custom",
		Name:  "custom",
		Path:  "/tmp/custom/SKILL.md",
		Scope: model.ScopeUser,
	}}
	m := uiModel{
		rows:     []tableRow{{Kind: rowSkill, Skill: skill}},
		rowIndex: 0,
	}

	updated, command := m.beginDelete()
	got := updated.(uiModel)

	if command != nil {
		t.Fatal("beginDelete() command != nil，打开确认框时不应立即删除")
	}
	if !got.deleteConfirm {
		t.Fatal("可删除 Skill 未打开删除确认框")
	}
	if got.deleteChoice != deleteChoiceCancel {
		t.Fatalf("deleteChoice = %d，默认应选中 Cancel", got.deleteChoice)
	}
	if got.deleteSkill.ID != skill.ID {
		t.Fatalf("deleteSkill.ID = %q，期望 %q", got.deleteSkill.ID, skill.ID)
	}
}

func TestDeletedMessageClearsPendingAndStartsReload(t *testing.T) {
	skill := service.SkillStatus{Skill: model.Skill{ID: "skill-a", Name: "Skill A"}}
	m := uiModel{
		deleteConfirm: true,
		deleting:      true,
		deleteNextID:  "skill-b",
		pending: map[string]pendingChange{
			"skill-a": {Desired: model.StateDisabled},
		},
		applied: map[string]bool{"skill-a": true},
	}

	updated, command := m.Update(deletedMsg{skill: skill})
	got := updated.(uiModel)

	if command == nil {
		t.Fatal("删除成功后未启动重新扫描")
	}
	if got.deleteConfirm || got.deleting {
		t.Fatal("删除成功后仍保留删除确认状态")
	}
	if _, ok := got.pending[skill.ID]; ok {
		t.Fatal("删除成功后仍保留 pending 变更")
	}
	if got.applied[skill.ID] {
		t.Fatal("删除成功后仍保留 applied 标记")
	}
	if !got.loading {
		t.Fatal("删除成功后未进入重新加载状态")
	}
	if got.selectAfterLoad != "skill-b" {
		t.Fatalf("selectAfterLoad = %q，期望重扫后选择相邻 Skill", got.selectAfterLoad)
	}
}

func TestDeletedMessageKeepsConfirmationOnFailure(t *testing.T) {
	wantErr := errors.New("permission denied")
	m := uiModel{deleteConfirm: true, deleting: true}

	updated, command := m.Update(deletedMsg{err: wantErr})
	got := updated.(uiModel)

	if command != nil {
		t.Fatal("删除失败后不应重新扫描")
	}
	if !got.deleteConfirm {
		t.Fatal("删除失败后确认框被关闭")
	}
	if got.deleteErr == nil || got.deleteErr.Error() != wantErr.Error() {
		t.Fatalf("deleteErr = %v，期望 %v", got.deleteErr, wantErr)
	}
}

func TestAdjacentSkillIDSkipsGroupHeaders(t *testing.T) {
	m := uiModel{
		rows: []tableRow{
			{Kind: rowGroup, GroupKey: "first"},
			{Kind: rowSkill, Skill: service.SkillStatus{Skill: model.Skill{ID: "first"}}},
			{Kind: rowGroup, GroupKey: "second"},
			{Kind: rowSkill, Skill: service.SkillStatus{Skill: model.Skill{ID: "second"}}},
		},
		rowIndex: 1,
	}

	if got := m.adjacentSkillID(); got != "second" {
		t.Fatalf("adjacentSkillID() = %q，期望跳过分组标题选择 %q", got, "second")
	}

	m.rowIndex = 3
	if got := m.adjacentSkillID(); got != "first" {
		t.Fatalf("adjacentSkillID() = %q，末尾删除时期望选择上一条 %q", got, "first")
	}
}
