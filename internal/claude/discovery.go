package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"skillctl/internal/model"
	"skillctl/internal/skillfs"
)

func Discover(cwd string) ([]model.Skill, model.DiscoveryReport, error) {
	report := model.DiscoveryReport{Status: model.DiscoveryComplete}
	roots, err := Roots(cwd)
	if err != nil {
		return nil, report, err
	}
	definitions, warnings, err := skillfs.Discover(roots)
	if err != nil {
		return nil, report, err
	}
	for _, warning := range warnings {
		report.Warnings = append(report.Warnings, model.DiscoveryWarning{Code: "filesystem_warning", Message: warning})
	}
	paths, err := Paths(cwd)
	if err != nil {
		return nil, report, err
	}
	if ManagedSettingsPresent(paths) {
		report.Status = model.DiscoveryPartialUnsupported
		report.Warnings = append(report.Warnings, model.DiscoveryWarning{
			Code:    "managed_settings_unsupported",
			Message: "Claude Managed Settings are present and remain outside skillctl effective-state calculation",
		})
	}

	winner := map[string]int{}
	for index, definition := range definitions {
		previous, ok := winner[definition.Name]
		if !ok || preferred(definition, definitions[previous]) {
			winner[definition.Name] = index
		}
	}

	skills := make([]model.Skill, 0, len(definitions))
	for index, definition := range definitions {
		effective, err := Effective(paths, definition.Name)
		if err != nil {
			return nil, report, err
		}
		state, err := StateForOverride(effective.Value)
		if err != nil {
			return nil, report, err
		}
		id := canonicalID(definition)
		skill := model.Skill{
			Agent:       model.AgentClaude,
			ID:          id,
			Name:        definition.Name,
			Description: definition.Description,
			Path:        definition.Path,
			Scope:       definition.Root.Scope,
			Source:      definition.Root.Source,
			Enabled:     state != model.StateDisabled,
			NativeState: state,
		}
		if winner[definition.Name] != index {
			active := definitions[winner[definition.Name]]
			skill.ReadOnly = true
			skill.Shadowed = true
			skill.BlockedBy = active.Path
		}
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	return skills, report, nil
}

func Roots(cwd string) ([]skillfs.Root, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	roots := []skillfs.Root{{
		Path:   filepath.Join(home, ".claude", "skills"),
		Anchor: home,
		Scope:  model.ScopeUser,
		Source: "claude",
	}}
	return append(roots, skillfs.ProjectRoots(cwd, filepath.Join(".claude", "skills"))...), nil
}

func TargetSettingsPath(cwd string, skill model.Skill) (string, error) {
	paths, err := Paths(cwd)
	if err != nil {
		return "", err
	}
	switch skill.Scope {
	case model.ScopeUser:
		return paths.User, nil
	case model.ScopeRepo:
		return paths.Local, nil
	default:
		return "", fmt.Errorf("%s skills are outside Claude skill management", skill.Scope)
	}
}

func HigherPriorityConflict(cwd string, skill model.Skill, desired model.InvocationState) (string, error) {
	if skill.Scope != model.ScopeUser {
		return "", nil
	}
	paths, err := Paths(cwd)
	if err != nil {
		return "", err
	}
	target, err := OverrideForState(desired)
	if err != nil {
		return "", err
	}
	layers := []struct {
		source string
		path   string
	}{
		{source: "project-local", path: paths.Local},
		{source: "project-shared", path: paths.Shared},
	}
	for _, layer := range layers {
		value, present, err := ReadOverride(layer.path, skill.Name)
		if err != nil {
			return "", err
		}
		if present && value != target {
			return fmt.Sprintf("%s setting in %s masks the requested user override", layer.source, layer.path), nil
		}
		if present {
			return "", nil
		}
	}
	return "", nil
}

func repositoryRoot(cwd string) string {
	return skillfs.RepositoryRoot(cwd)
}

func preferred(left, right skillfs.Definition) bool {
	if left.Root.Scope != right.Root.Scope {
		return left.Root.Scope == model.ScopeUser
	}
	if left.Root.Scope == model.ScopeRepo {
		return len(left.Root.ProjectKey) < len(right.Root.ProjectKey)
	}
	return left.Path < right.Path
}

func canonicalID(definition skillfs.Definition) string {
	if definition.Root.Scope == model.ScopeUser {
		return fmt.Sprintf("claude:user:%s:%s", definition.Root.Source, definition.Name)
	}
	return fmt.Sprintf("claude:repo:%s:%s", definition.Root.ProjectKey, definition.Name)
}
