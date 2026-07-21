package service

import (
	"skillctl/internal/codex"
	"skillctl/internal/model"
)

// DeleteSkill 删除用户级或项目级 Skill 的完整目录。
func (m Manager) DeleteSkill(skill model.Skill) error {
	return codex.DeleteSkill(m.CWD, skill)
}
