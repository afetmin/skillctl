package skillfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"skillctl/internal/model"
)

type Root struct {
	Path       string
	Anchor     string
	Scope      model.Scope
	Source     string
	ProjectKey string
}

type Definition struct {
	Name        string
	Description string
	Path        string
	Root        Root
}

func RepositoryRoot(cwd string) string {
	current, err := filepath.Abs(cwd)
	if err != nil {
		return filepath.Clean(cwd)
	}
	for {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(cwd)
		}
		current = parent
	}
}

func ProjectRoots(cwd string, relative string) []Root {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		cwd = filepath.Clean(cwd)
	}
	repository := RepositoryRoot(cwd)
	var directories []string
	for current := cwd; ; current = filepath.Dir(current) {
		directories = append(directories, current)
		if current == repository || filepath.Dir(current) == current {
			break
		}
	}
	for left, right := 0, len(directories)-1; left < right; left, right = left+1, right-1 {
		directories[left], directories[right] = directories[right], directories[left]
	}
	result := make([]Root, 0, len(directories))
	for _, directory := range directories {
		relativeDirectory, _ := filepath.Rel(repository, directory)
		if relativeDirectory == "." {
			relativeDirectory = ""
		}
		projectKey := filepath.Base(repository)
		if relativeDirectory != "" {
			projectKey += ":" + filepath.ToSlash(relativeDirectory)
		}
		result = append(result, Root{
			Path:       filepath.Join(directory, relative),
			Anchor:     repository,
			Scope:      model.ScopeRepo,
			Source:     filepath.ToSlash(filepath.Join(directory, relative)),
			ProjectKey: projectKey,
		})
	}
	return result
}

func Discover(roots []Root) ([]Definition, []string, error) {
	var result []Definition
	var warnings []string
	seen := map[string]bool{}
	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("scan %s: %v", root.Path, err))
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			skillPath := filepath.Join(root.Path, entry.Name(), "SKILL.md")
			if seen[skillPath] {
				continue
			}
			info, err := os.Stat(skillPath)
			if errors.Is(err, os.ErrNotExist) || (err == nil && info.IsDir()) {
				continue
			}
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("inspect %s: %v", skillPath, err))
				continue
			}
			seen[skillPath] = true
			name, description, err := ReadFrontmatter(skillPath)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", skillPath, err))
				continue
			}
			result = append(result, Definition{
				Name:        name,
				Description: description,
				Path:        skillPath,
				Root:        root,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, warnings, nil
}

func ReadFrontmatter(path string) (string, string, error) {
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
		metadata.Name = filepath.Base(filepath.Dir(path))
	}
	return metadata.Name, metadata.Description, nil
}

func Delete(skill model.Skill, roots []Root) error {
	if skill.Scope != model.ScopeUser && skill.Scope != model.ScopeRepo {
		return fmt.Errorf("%s skills cannot be deleted", skill.Scope)
	}
	path := filepath.Clean(skill.Path)
	if filepath.Base(path) != "SKILL.md" {
		return fmt.Errorf("skill path must point to SKILL.md: %s", skill.Path)
	}
	target := filepath.Dir(path)
	for _, root := range roots {
		if root.Scope != skill.Scope || !pathWithinRoot(target, root.Path) || filepath.Clean(target) == filepath.Clean(root.Path) {
			continue
		}
		if err := rejectSymlinkAncestors(root.Anchor, target); err != nil {
			return err
		}
		if _, err := os.Lstat(target); err != nil {
			return fmt.Errorf("inspect skill %q: %w", skill.Name, err)
		}
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("delete skill %q: %w", skill.Name, err)
		}
		return nil
	}
	return fmt.Errorf("skill path is outside deletable roots: %s", skill.Path)
}

func pathWithinRoot(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func rejectSymlinkAncestors(anchor, target string) error {
	relative, err := filepath.Rel(filepath.Clean(anchor), filepath.Clean(target))
	if err != nil {
		return fmt.Errorf("resolve delete path: %w", err)
	}
	current := filepath.Clean(anchor)
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
