package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"skillctl/internal/model"
)

type skillDeleteRoot struct {
	path   string
	anchor string
}

// DeleteSkill 根据 Codex Skill 的来源规则安全删除目录或目录符号链接。
func DeleteSkill(cwd string, skill model.Skill) error {
	target, err := skillDeleteTarget(cwd, skill)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("delete skill %q: %w", skill.Name, err)
	}
	return nil
}

func skillDeleteTarget(cwd string, skill model.Skill) (string, error) {
	if skill.Scope != model.ScopeUser && skill.Scope != model.ScopeRepo {
		return "", fmt.Errorf("%s skills cannot be deleted", skill.Scope)
	}
	path := filepath.Clean(skill.Path)
	if filepath.Base(path) != "SKILL.md" {
		return "", fmt.Errorf("skill path must point to SKILL.md: %s", skill.Path)
	}
	target := filepath.Dir(path)
	roots, err := skillDeleteRoots(cwd, skill.Scope)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if pathWithinRoot(target, root.path) && filepath.Clean(target) != filepath.Clean(root.path) {
			if err := rejectSymlinkAncestors(root.anchor, target); err != nil {
				return "", err
			}
			if _, err := os.Lstat(path); err != nil {
				return "", fmt.Errorf("inspect skill %q: %w", skill.Name, err)
			}
			return target, nil
		}
	}
	return "", fmt.Errorf("skill path is outside deletable roots: %s", skill.Path)
}

func skillDeleteRoots(cwd string, scope model.Scope) ([]skillDeleteRoot, error) {
	if scope == model.ScopeRepo {
		if cwd == "" {
			return nil, fmt.Errorf("project root is not configured")
		}
		return []skillDeleteRoot{{path: filepath.Join(cwd, ".agents", "skills"), anchor: cwd}}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home: %w", err)
	}
	return []skillDeleteRoot{
		{path: filepath.Join(home, ".agents", "skills"), anchor: home},
		{path: filepath.Join(home, ".codex", "skills"), anchor: home},
		{path: filepath.Join(home, ".codex", "superpowers", "skills"), anchor: home},
		{path: filepath.Join(home, ".claude", "skills"), anchor: home},
		{path: filepath.Join(home, ".cc-switch", "skills"), anchor: home},
	}, nil
}

func pathWithinRoot(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func rejectSymlinkAncestors(root, target string) error {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return fmt.Errorf("resolve delete path: %w", err)
	}
	current := filepath.Clean(root)
	info, err := os.Lstat(current)
	if err != nil {
		return fmt.Errorf("inspect delete path %s: %w", current, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill path crosses symlinked directory: %s", current)
	}
	parts := strings.Split(relative, string(filepath.Separator))
	for index := 0; index < len(parts)-1; index++ {
		if index > 0 || parts[index] != "." {
			current = filepath.Join(current, parts[index])
		}
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect delete path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill path crosses symlinked directory: %s", current)
		}
	}
	return nil
}
