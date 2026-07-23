package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"skillctl/internal/model"
	"skillctl/internal/policy"
	"skillctl/internal/skillfs"
)

func DiscoverSupportedFilesystem(cwd string) ([]model.Skill, model.DiscoveryReport, error) {
	report := model.DiscoveryReport{Status: model.DiscoveryComplete}
	roots, err := SupportedRoots(cwd)
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
	skills := make([]model.Skill, 0, len(definitions))
	for _, definition := range definitions {
		policyPath := filepath.Join(filepath.Dir(definition.Path), "agents", "openai.yaml")
		allow, err := policy.Read(policyPath)
		if err != nil {
			report.Warnings = append(report.Warnings, model.DiscoveryWarning{
				Code:    "policy_warning",
				Message: fmt.Sprintf("%s: %v", definition.Path, err),
			})
			continue
		}
		skills = append(skills, model.Skill{
			Agent:       model.AgentCodex,
			ID:          supportedID(definition),
			Name:        definition.Name,
			Description: definition.Description,
			Path:        definition.Path,
			Scope:       definition.Root.Scope,
			Source:      definition.Root.Source,
			Enabled:     true,
			Policy:      allow,
			PolicyPath:  policyPath,
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	return skills, report, nil
}

func SupportedRoots(cwd string) ([]skillfs.Root, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	roots := []skillfs.Root{
		{
			Path:   filepath.Join(home, ".agents", "skills"),
			Anchor: home,
			Scope:  model.ScopeUser,
			Source: "agents",
		},
		{
			Path:   filepath.Join(home, ".codex", "skills"),
			Anchor: home,
			Scope:  model.ScopeUser,
			Source: "codex",
		},
	}
	return append(roots, skillfs.ProjectRoots(cwd, filepath.Join(".agents", "skills"))...), nil
}

func ApplyEnablement(skills []model.Skill, enablement SkillEnablement) []model.Skill {
	for index := range skills {
		if enabled, ok := enablement.Paths[skills[index].Path]; ok {
			skills[index].Enabled = enabled
		}
	}
	return skills
}

func supportedID(definition skillfs.Definition) string {
	if definition.Root.Scope == model.ScopeUser {
		return fmt.Sprintf("codex:user:%s:%s", definition.Root.Source, definition.Name)
	}
	return fmt.Sprintf("codex:repo:%s:%s", definition.Root.ProjectKey, definition.Name)
}
