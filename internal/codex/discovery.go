package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"skillctl/internal/model"
	"skillctl/internal/policy"
)

func Discover(client *Client, cwd string) ([]model.Skill, []string, error) {
	metadata, warnings, err := client.ListSkills([]string{cwd})
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	var skills []model.Skill
	for _, item := range metadata {
		if seen[item.Path] {
			continue
		}
		seen[item.Path] = true
		skill, err := fromMetadata(item)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", item.Path, err))
			continue
		}
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	return skills, warnings, nil
}

func fromMetadata(item skillMetadata) (model.Skill, error) {
	policyPath := filepath.Join(filepath.Dir(item.Path), "agents", "openai.yaml")
	allow, err := policy.Read(policyPath)
	if err != nil {
		return model.Skill{}, err
	}
	scope, source := classify(item.Path, item.Scope)
	return model.Skill{
		ID:          canonicalID(item.Name, item.Path, scope, source),
		Name:        item.Name,
		Description: item.Description,
		Path:        item.Path,
		Scope:       scope,
		Source:      source,
		Enabled:     item.Enabled,
		Policy:      allow,
		PolicyPath:  policyPath,
	}, nil
}

func DiscoverFilesystem(cwd string) ([]model.Skill, []string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	roots := []struct {
		path  string
		scope string
	}{
		{path: filepath.Join(home, ".agents", "skills"), scope: "user"},
		{path: filepath.Join(home, ".codex", "skills"), scope: "user"},
		{path: filepath.Join(home, ".codex", "plugins", "cache"), scope: "user"},
		{path: filepath.Join(cwd, ".agents", "skills"), scope: "repo"},
	}
	var skills []model.Skill
	var warnings []string
	seen := map[string]bool{}
	for _, root := range roots {
		if _, err := os.Stat(root.path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			warnings = append(warnings, fmt.Sprintf("scan %s: %v", root.path, err))
			continue
		}
		err := filepath.WalkDir(root.path, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				warnings = append(warnings, walkErr.Error())
				return nil
			}
			if entry.IsDir() || entry.Name() != "SKILL.md" || seen[path] {
				return nil
			}
			seen[path] = true
			name, description, err := readFrontmatter(path)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
				return nil
			}
			metadata := skillMetadata{Name: name, Description: description, Path: path, Scope: root.scope, Enabled: true}
			skill, err := fromMetadata(metadata)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
				return nil
			}
			skills = append(skills, skill)
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			warnings = append(warnings, fmt.Sprintf("scan %s: %v", root.path, err))
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	return skills, warnings, nil
}

func classify(path, reported string) (model.Scope, string) {
	clean := filepath.ToSlash(path)
	if strings.Contains(clean, "/.codex/skills/.system/") {
		return model.ScopeSystem, "system"
	}
	if marker := "/.codex/plugins/cache/"; strings.Contains(clean, marker) {
		relative := strings.SplitN(clean, marker, 2)[1]
		parts := strings.Split(relative, "/")
		if len(parts) >= 2 {
			return model.ScopePlugin, parts[0] + ":" + parts[1]
		}
		return model.ScopePlugin, "plugin"
	}
	switch reported {
	case "system":
		return model.ScopeSystem, "system"
	case "admin":
		return model.ScopeAdmin, "admin"
	case "repo":
		return model.ScopeRepo, "repo"
	case "user":
		return model.ScopeUser, userSource(clean)
	}
	if strings.Contains(clean, "/.agents/skills/") || strings.Contains(clean, "/.codex/skills/") {
		return model.ScopeUser, userSource(clean)
	}
	return model.ScopeOther, "other"
}

func userSource(clean string) string {
	if strings.Contains(clean, "/.agents/skills/") {
		return "agents"
	}
	if strings.Contains(clean, "/.codex/skills/") {
		return "codex"
	}
	if strings.Contains(clean, "/.codex/superpowers/skills/") {
		return "codex-superpowers"
	}
	if strings.Contains(clean, "/.claude/skills/") {
		return "claude"
	}
	if strings.Contains(clean, "/.cc-switch/skills/") {
		return "cc-switch"
	}
	if home, err := os.UserHomeDir(); err == nil {
		home = filepath.ToSlash(home)
		if relative, ok := strings.CutPrefix(clean, home+"/"); ok {
			parts := strings.Split(relative, "/")
			if len(parts) > 0 {
				source := strings.TrimPrefix(parts[0], ".")
				if source == "codex" && len(parts) > 1 {
					source += "-" + strings.TrimPrefix(parts[1], ".")
				}
				if source != "" {
					return source
				}
			}
		}
	}
	return "user"
}

func canonicalID(name, path string, scope model.Scope, source string) string {
	switch scope {
	case model.ScopeUser, model.ScopePlugin:
		return fmt.Sprintf("codex:%s:%s:%s", scope, source, name)
	case model.ScopeRepo:
		return fmt.Sprintf("codex:repo:%s:%s", repoSource(path), name)
	default:
		return fmt.Sprintf("codex:%s:%s", scope, name)
	}
}

func repoSource(path string) string {
	clean := filepath.ToSlash(path)
	if marker := "/.agents/skills/"; strings.Contains(clean, marker) {
		prefix := strings.SplitN(clean, marker, 2)[0]
		if base := filepath.Base(prefix); base != "" && base != "." {
			return base
		}
	}
	return "project"
}

func readFrontmatter(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", "", errors.New("missing YAML frontmatter")
	}
	lines := strings.Split(text, "\n")
	end := -1
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			end = index
			break
		}
	}
	if end < 0 {
		return "", "", errors.New("unterminated YAML frontmatter")
	}
	var metadata struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &metadata); err != nil {
		return "", "", err
	}
	if metadata.Name == "" {
		return "", "", errors.New("frontmatter name is required")
	}
	return metadata.Name, metadata.Description, nil
}
