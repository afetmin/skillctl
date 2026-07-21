package service

import (
	"os"
	"path/filepath"
	"testing"

	"skillctl/internal/model"
)

func TestDeleteSkillRemovesUserSkillDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	skillDir := filepath.Join(home, ".agents", "skills", "custom")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(filepath.Join(skillDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: custom\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "assets", "example.txt"), []byte("example"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := (Manager{}).DeleteSkill(model.Skill{
		ID:    "codex:user:agents:custom",
		Name:  "custom",
		Path:  skillPath,
		Scope: model.ScopeUser,
	})
	if err != nil {
		t.Fatalf("DeleteSkill() error = %v", err)
	}
	if _, err := os.Lstat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("Skill 目录仍然存在，Lstat() error = %v", err)
	}
}

func TestDeleteSkillRemovesSymlinkWithoutDeletingTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	realDir := filepath.Join(t.TempDir(), "shared-skill")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realSkillPath := filepath.Join(realDir, "SKILL.md")
	if err := os.WriteFile(realSkillPath, []byte("---\nname: shared\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "shared")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}

	err := (Manager{}).DeleteSkill(model.Skill{
		ID:    "codex:user:agents:shared",
		Name:  "shared",
		Path:  filepath.Join(linkDir, "SKILL.md"),
		Scope: model.ScopeUser,
	})
	if err != nil {
		t.Fatalf("DeleteSkill() error = %v", err)
	}
	if _, err := os.Lstat(linkDir); !os.IsNotExist(err) {
		t.Fatalf("Skill 符号链接仍然存在，Lstat() error = %v", err)
	}
	if _, err := os.Stat(realSkillPath); err != nil {
		t.Fatalf("符号链接指向的真实 Skill 被删除: %v", err)
	}
}

func TestDeleteSkillRejectsProtectedScopes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, scope := range []model.Scope{
		model.ScopeSystem,
		model.ScopePlugin,
		model.ScopeAdmin,
		model.ScopeOther,
	} {
		t.Run(string(scope), func(t *testing.T) {
			skillDir := filepath.Join(home, ".agents", "skills", string(scope))
			skillPath := filepath.Join(skillDir, "SKILL.md")
			if err := os.MkdirAll(skillDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(skillPath, []byte("---\nname: protected\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			err := (Manager{}).DeleteSkill(model.Skill{
				Name:  "protected",
				Path:  skillPath,
				Scope: scope,
			})
			if err == nil {
				t.Fatal("DeleteSkill() error = nil，期望拒绝删除受保护 Skill")
			}
			if _, err := os.Stat(skillPath); err != nil {
				t.Fatalf("受保护 Skill 被修改: %v", err)
			}
		})
	}
}

func TestDeleteSkillRejectsUserSkillOutsideKnownRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	skillDir := filepath.Join(t.TempDir(), "untrusted")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: untrusted\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := (Manager{}).DeleteSkill(model.Skill{
		Name:  "untrusted",
		Path:  skillPath,
		Scope: model.ScopeUser,
	})
	if err == nil {
		t.Fatal("DeleteSkill() error = nil，期望拒绝删除已知根目录之外的 Skill")
	}
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("根目录之外的 Skill 被修改: %v", err)
	}
}

func TestDeleteSkillRejectsSymlinkAncestor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	externalRoot := t.TempDir()
	realSkillDir := filepath.Join(externalRoot, "custom")
	realSkillPath := filepath.Join(realSkillDir, "SKILL.md")
	if err := os.MkdirAll(realSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realSkillPath, []byte("---\nname: custom\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "bundle")
	if err := os.Symlink(externalRoot, linkedParent); err != nil {
		t.Fatal(err)
	}

	err := (Manager{}).DeleteSkill(model.Skill{
		Name:  "custom",
		Path:  filepath.Join(linkedParent, "custom", "SKILL.md"),
		Scope: model.ScopeUser,
	})
	if err == nil {
		t.Fatal("DeleteSkill() error = nil，期望拒绝穿过上级符号链接删除")
	}
	if _, err := os.Stat(realSkillPath); err != nil {
		t.Fatalf("上级符号链接指向的真实 Skill 被删除: %v", err)
	}
}

func TestDeleteSkillRemovesProjectSkillDirectory(t *testing.T) {
	projectRoot := t.TempDir()
	skillDir := filepath.Join(projectRoot, ".agents", "skills", "project-skill")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: project-skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := (Manager{CWD: projectRoot}).DeleteSkill(model.Skill{
		Name:  "project-skill",
		Path:  skillPath,
		Scope: model.ScopeRepo,
	})
	if err != nil {
		t.Fatalf("DeleteSkill() error = %v", err)
	}
	if _, err := os.Lstat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("项目级 Skill 目录仍然存在，Lstat() error = %v", err)
	}
}

func TestDeleteSkillRejectsSymlinkInsideConfiguredRootPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	externalAgents := t.TempDir()
	skillDir := filepath.Join(externalAgents, "skills", "custom")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: custom\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalAgents, filepath.Join(home, ".agents")); err != nil {
		t.Fatal(err)
	}

	err := (Manager{}).DeleteSkill(model.Skill{
		Name:  "custom",
		Path:  filepath.Join(home, ".agents", "skills", "custom", "SKILL.md"),
		Scope: model.ScopeUser,
	})
	if err == nil {
		t.Fatal("DeleteSkill() error = nil，期望拒绝穿过 Skill 根路径中的符号链接")
	}
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("根路径符号链接指向的真实 Skill 被删除: %v", err)
	}
}
