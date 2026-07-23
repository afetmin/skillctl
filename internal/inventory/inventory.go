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
	CategoryPersonal Category = "personal"
	CategoryProject  Category = "project"
)

var categoryOrder = map[Category]int{
	CategoryPersonal: 0,
	CategoryProject:  1,
}

type Summary struct {
	Total    int `json:"total"`
	Implicit int `json:"implicit"`
	NameOnly int `json:"name_only"`
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
	State     model.InvocationState
	Drift     bool
	Scope     model.Scope
	Source    string
	SourceKey string
	Query     string
}

type SourceOption struct {
	Key      string
	Label    string
	Category Category
	GroupKey string
	Depth    int
	Count    int
}

type StateOption struct {
	State model.InvocationState
	Label string
	Count int
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
		if !matchesSourceKey(item, filter.SourceKey) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{item.Name, item.Description, item.ID, item.Path}, "\n"))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}

func matchesSourceKey(item service.SkillStatus, sourceKey string) bool {
	if sourceKey == "" || sourceKey == "all" {
		return true
	}
	category, groupKey, _ := location(item)
	return sourceKey == "category:"+string(category) || sourceKey == "group:"+groupKey
}

func GroupStatuses(items []service.SkillStatus) []Group {
	byKey := map[string]*Group{}
	for _, item := range items {
		if item.Scope != model.ScopeUser && item.Scope != model.ScopeRepo {
			continue
		}
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
	for _, category := range []Category{CategoryPersonal, CategoryProject} {
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

func SourceOptions(items []service.SkillStatus, filter Filter) []SourceOption {
	options := Options(GroupStatuses(items))
	filter.Source = ""
	filter.SourceKey = ""
	matching := Options(GroupStatuses(Apply(items, filter)))
	counts := make(map[string]int, len(matching))
	for _, option := range matching {
		counts[option.Key] = option.Count
	}
	for index := range options {
		options[index].Count = counts[options[index].Key]
	}
	return options
}

func StateOptions(items []service.SkillStatus, filter Filter, valid []model.InvocationState) []StateOption {
	filter.State = ""
	matching := Apply(items, filter)
	summary := Summary{}
	for _, item := range matching {
		addSummary(&summary, item)
	}
	result := []StateOption{{Label: "All", Count: summary.Total}}
	counts := map[model.InvocationState]int{
		model.StateImplicit: summary.Implicit,
		model.StateNameOnly: summary.NameOnly,
		model.StateManual:   summary.Manual,
		model.StateDisabled: summary.Disabled,
	}
	for _, state := range valid {
		result = append(result, StateOption{State: state, Label: stateLabel(state), Count: counts[state]})
	}
	return result
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
	case CategoryPersonal:
		return "Personal"
	case CategoryProject:
		return "Project"
	default:
		return "Unsupported"
	}
}

func SummaryLine(summary Summary, agent model.Agent) string {
	if agent == model.AgentClaude {
		return fmt.Sprintf("%d skills · implicit %d · name-only %d · manual %d · disabled %d · drift %d",
			summary.Total, summary.Implicit, summary.NameOnly, summary.Manual, summary.Disabled, summary.Drift)
	}
	return fmt.Sprintf("%d skills · implicit %d · manual %d · disabled %d · drift %d",
		summary.Total, summary.Implicit, summary.Manual, summary.Disabled, summary.Drift)
}

func addSummary(summary *Summary, item service.SkillStatus) {
	summary.Total++
	switch item.Actual {
	case model.StateImplicit:
		summary.Implicit++
	case model.StateNameOnly:
		summary.NameOnly++
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
	case model.ScopeRepo:
		label := projectRoot(item.Path)
		return CategoryProject, "project:" + label, label
	case model.ScopeUser:
		label := personalLabel(item.Source)
		return CategoryPersonal, "personal:" + item.Source, label
	}
	return "", "", ""
}

func personalLabel(source string) string {
	switch source {
	case "agents":
		return "~/.agents/skills"
	case "codex":
		return "~/.codex/skills"
	case "claude":
		return "~/.claude/skills"
	default:
		return source
	}
}

func projectRoot(path string) string {
	clean := filepath.ToSlash(path)
	if prefix, _, ok := strings.Cut(clean, "/.agents/skills/"); ok {
		return prefix + "/.agents/skills"
	}
	if prefix, _, ok := strings.Cut(clean, "/.claude/skills/"); ok {
		return prefix + "/.claude/skills"
	}
	return filepath.Dir(path)
}

func stateLabel(state model.InvocationState) string {
	switch state {
	case model.StateImplicit:
		return "Implicit"
	case model.StateNameOnly:
		return "Name only"
	case model.StateManual:
		return "Manual"
	case model.StateDisabled:
		return "Disabled"
	default:
		return string(state)
	}
}
