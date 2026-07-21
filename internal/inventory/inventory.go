package inventory

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"skillctl/internal/model"
	"skillctl/internal/service"
)

type Category string

const (
	CategorySystem   Category = "system"
	CategoryPersonal Category = "personal"
	CategoryPlugins  Category = "plugins"
	CategoryProject  Category = "project"
	CategoryOther    Category = "other"
)

var categoryOrder = map[Category]int{
	CategorySystem:   0,
	CategoryPersonal: 1,
	CategoryPlugins:  2,
	CategoryProject:  3,
	CategoryOther:    4,
}

type Summary struct {
	Total    int `json:"total"`
	Implicit int `json:"implicit"`
	Manual   int `json:"manual"`
	Disabled int `json:"disabled"`
	Drift    int `json:"drift"`
}

type Group struct {
	Key      string                `json:"key"`
	Category Category              `json:"category"`
	Label    string                `json:"label"`
	Summary  Summary               `json:"summary"`
	Skills   []service.SkillStatus `json:"skills"`
}

type Filter struct {
	State  model.InvocationState
	Drift  bool
	Scope  model.Scope
	Source string
	Query  string
}

type SourceOption struct {
	Key      string
	Label    string
	Category Category
	GroupKey string
	Depth    int
	Count    int
}

func Apply(items []service.SkillStatus, filter Filter) []service.SkillStatus {
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	source := strings.ToLower(strings.TrimSpace(filter.Source))
	result := make([]service.SkillStatus, 0, len(items))
	for _, item := range items {
		if filter.State != "" && item.Actual != filter.State {
			continue
		}
		if filter.Drift && item.Actual == item.Desired {
			continue
		}
		if filter.Scope != "" && item.Scope != filter.Scope {
			continue
		}
		if source != "" && !strings.Contains(strings.ToLower(item.Source), source) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{item.Name, item.ID, item.Source, item.Path}, "\n"))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}

func GroupStatuses(items []service.SkillStatus) []Group {
	byKey := map[string]*Group{}
	for _, item := range items {
		category, key, label := location(item)
		group := byKey[key]
		if group == nil {
			group = &Group{Key: key, Category: category, Label: label, Skills: []service.SkillStatus{}}
			byKey[key] = group
		}
		group.Skills = append(group.Skills, item)
		addSummary(&group.Summary, item)
	}
	groups := make([]Group, 0, len(byKey))
	for _, group := range byKey {
		sort.Slice(group.Skills, func(i, j int) bool {
			if group.Skills[i].Name == group.Skills[j].Name {
				return group.Skills[i].ID < group.Skills[j].ID
			}
			return strings.ToLower(group.Skills[i].Name) < strings.ToLower(group.Skills[j].Name)
		})
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		left := categoryOrder[groups[i].Category]
		right := categoryOrder[groups[j].Category]
		if left != right {
			return left < right
		}
		return strings.ToLower(groups[i].Label) < strings.ToLower(groups[j].Label)
	})
	return groups
}

func Options(groups []Group) []SourceOption {
	options := []SourceOption{{Key: "all", Label: "All", Count: total(groups)}}
	for _, category := range []Category{CategorySystem, CategoryPersonal, CategoryPlugins, CategoryProject, CategoryOther} {
		count := 0
		var matching []Group
		for _, group := range groups {
			if group.Category == category {
				count += group.Summary.Total
				matching = append(matching, group)
			}
		}
		if count == 0 {
			continue
		}
		options = append(options, SourceOption{
			Key:      "category:" + string(category),
			Label:    CategoryTitle(category),
			Category: category,
			Depth:    0,
			Count:    count,
		})
		for _, group := range matching {
			options = append(options, SourceOption{
				Key:      "group:" + group.Key,
				Label:    group.Label,
				Category: category,
				GroupKey: group.Key,
				Depth:    1,
				Count:    group.Summary.Total,
			})
		}
	}
	return options
}

func Select(groups []Group, option SourceOption) []Group {
	if option.Key == "all" {
		return groups
	}
	var result []Group
	for _, group := range groups {
		if option.GroupKey != "" && group.Key == option.GroupKey {
			result = append(result, group)
		} else if option.GroupKey == "" && group.Category == option.Category {
			result = append(result, group)
		}
	}
	return result
}

func CategoryTitle(category Category) string {
	switch category {
	case CategorySystem:
		return "System"
	case CategoryPersonal:
		return "Personal"
	case CategoryPlugins:
		return "Plugins"
	case CategoryProject:
		return "Project"
	default:
		return "Other"
	}
}

func SummaryLine(summary Summary) string {
	return fmt.Sprintf("%d skills · implicit %d · manual %d · disabled %d · drift %d",
		summary.Total, summary.Implicit, summary.Manual, summary.Disabled, summary.Drift)
}

func addSummary(summary *Summary, item service.SkillStatus) {
	summary.Total++
	switch item.Actual {
	case model.StateImplicit:
		summary.Implicit++
	case model.StateManual:
		summary.Manual++
	case model.StateDisabled:
		summary.Disabled++
	}
	if item.Actual != item.Desired {
		summary.Drift++
	}
}

func total(groups []Group) int {
	result := 0
	for _, group := range groups {
		result += group.Summary.Total
	}
	return result
}

func location(item service.SkillStatus) (Category, string, string) {
	switch item.Scope {
	case model.ScopeSystem:
		return CategorySystem, "system:codex", "~/.codex/skills/.system"
	case model.ScopePlugin:
		label := strings.ReplaceAll(item.Source, ":", "/")
		return CategoryPlugins, "plugin:" + item.Source, label
	case model.ScopeRepo:
		label := projectRoot(item.Path)
		return CategoryProject, "project:" + label, label
	case model.ScopeUser:
		label := personalLabel(item.Source)
		return CategoryPersonal, "personal:" + item.Source, label
	default:
		label := item.Source
		if label == "" {
			label = filepath.Dir(item.Path)
		}
		return CategoryOther, "other:" + label, label
	}
}

func personalLabel(source string) string {
	switch source {
	case "agents":
		return "~/.agents/skills"
	case "codex":
		return "~/.codex/skills"
	case "claude":
		return "~/.claude/skills"
	case "cc-switch":
		return "~/.cc-switch/skills"
	case "codex-superpowers":
		return "~/.codex/superpowers/skills"
	default:
		return source
	}
}

func projectRoot(path string) string {
	clean := filepath.ToSlash(path)
	if prefix, _, ok := strings.Cut(clean, "/.agents/skills/"); ok {
		return prefix + "/.agents/skills"
	}
	return filepath.Dir(path)
}
