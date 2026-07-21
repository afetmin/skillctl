package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"skillctl/internal/inventory"
	"skillctl/internal/model"
	"skillctl/internal/service"
)

var (
	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	mutedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	headingStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	selectedStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24"))
	footerKeyStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("228"))
	footerTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	helpStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("39")).Padding(1, 2)
	groupStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	warnStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	implicitStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	manualStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	disabledStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func (m uiModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "Loading skillctl..."
	}
	if m.help {
		return m.helpView()
	}
	if m.confirm {
		return m.confirmView()
	}
	if m.detail {
		return m.detailView()
	}
	return m.mainView()
}

func (m uiModel) mainView() string {
	leftWidth, rightWidth, _, mainHeight := m.layout()
	lines := []string{m.headerView(), m.searchView()}
	right := m.tableView(rightWidth, mainHeight)
	if leftWidth == 0 {
		lines = append(lines, right...)
	} else {
		left := m.sourceView(leftWidth, mainHeight)
		for index := 0; index < mainHeight; index++ {
			lines = append(lines, fitLine(left[index], leftWidth)+mutedStyle.Render(" │ ")+fitLine(right[index], rightWidth))
		}
	}
	lines = append(lines, m.footerView())
	return strings.Join(lines, "\n")
}

func (m uiModel) headerView() string {
	summary := summarizeGroups(m.groups)
	line := titleStyle.Render("skillctl") + "  " + mutedStyle.Render(inventory.SummaryLine(summary))
	var badges []string
	if len(m.pending) > 0 {
		badges = append(badges, warnStyle.Render(fmt.Sprintf("%d pending", len(m.pending))))
	}
	if summary.Drift > 0 {
		badges = append(badges, errorStyle.Render(fmt.Sprintf("%d drift", summary.Drift)))
	}
	if m.loading || m.applying {
		badges = append(badges, m.spinner.View()+" working")
	}
	if len(badges) > 0 {
		line += "  " + strings.Join(badges, "  ")
	}
	return fitLine(line, m.width)
}

func (m uiModel) searchView() string {
	if m.searching {
		input := m.search
		input.Width = max(10, m.width-4)
		return fitLine(input.View(), m.width)
	}
	selected := "All"
	if m.sourceIndex >= 0 && m.sourceIndex < len(m.sources) {
		selected = m.sources[m.sourceIndex].Label
	}
	query := ""
	if m.search.Value() != "" {
		query = "  search: " + m.search.Value()
	}
	return fitLine(mutedStyle.Render("Source: ")+selected+query, m.width)
}

func (m uiModel) sourceView(width, height int) []string {
	lines := []string{headingStyle.Render("SOURCES")}
	available := max(0, height-1)
	end := min(len(m.sources), m.sourceOffset+available)
	for index := m.sourceOffset; index < end; index++ {
		option := m.sources[index]
		prefix := "  "
		if option.Depth > 0 {
			prefix = "    "
		}
		labelWidth := max(3, width-lipgloss.Width(prefix)-6)
		line := prefix + truncateEnd(option.Label, labelWidth)
		line = padRight(line, width-5) + fmt.Sprintf("%4d", option.Count)
		if m.focus == 0 && index == m.sourceIndex {
			line = selectedStyle.Render(fitLine(line, width))
		}
		lines = append(lines, line)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:height]
}

func (m uiModel) tableView(width, height int) []string {
	label := "All skills"
	if m.sourceIndex >= 0 && m.sourceIndex < len(m.sources) {
		label = m.sources[m.sourceIndex].Label
	}
	lines := []string{headingStyle.Render(fitLine(label, width)), mutedStyle.Render(m.tableHeader(width))}
	available := max(0, height-2)
	end := min(len(m.rows), m.rowOffset+available)
	for index := m.rowOffset; index < end; index++ {
		row := m.rows[index]
		var line string
		if row.Kind == rowGroup {
			indicator := "▾"
			if m.collapsed[row.GroupKey] {
				indicator = "▸"
			}
			line = fmt.Sprintf("%s %s / %s  %s", indicator, inventory.CategoryTitle(row.Group.Category), row.Group.Label, inventory.SummaryLine(row.Group.Summary))
			line = groupStyle.Render(fitLine(line, width))
		} else {
			line = m.skillLine(row.Skill, width)
		}
		if m.focus == 1 && index == m.rowIndex {
			line = selectedStyle.Render(fitLine(ansi.Strip(line), width))
		}
		lines = append(lines, line)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:height]
}

func (m uiModel) tableHeader(width int) string {
	nameWidth, pathWidth, showPath := tableWidths(width)
	header := fmt.Sprintf("%-*s %-10s %-10s %-9s %-7s", nameWidth, "NAME", "ACTUAL", "DESIRED", "STATUS", "MANAGED")
	if showPath {
		header += " " + padRight("PATH", pathWidth)
	}
	return fitLine(header, width)
}

func (m uiModel) skillLine(skill service.SkillStatus, width int) string {
	presentation := m.presentationFor(skill)
	managed := "no"
	if skill.Managed {
		managed = "yes"
	}
	nameWidth, pathWidth, showPath := tableWidths(width)
	nameWidth = max(3, nameWidth)
	name := renderMarker(presentation.Marker, skill.Actual) + " " + padRight(truncateEnd(skill.Name, nameWidth-2), nameWidth-2)
	line := fmt.Sprintf("%s %-10s %-10s %-9s %-7s", name, skill.Actual, presentation.Desired, presentation.Status, managed)
	if showPath {
		line += " " + middleTruncate(skill.Path, pathWidth)
	}
	return fitLine(line, width)
}

func (m uiModel) footerView() string {
	status := m.status
	if m.err != nil {
		status = errorStyle.Render(m.err.Error())
	}
	hints := []keyHint{
		{"Tab", "pane"}, {"↑/↓", "move"}, {"Enter", "open"}, {"i/m/d", "stage"},
		{"a", "apply"}, {"/", "search"}, {"h", "help"}, {"q", "quit"},
	}
	if leftWidth, _, _, _ := m.layout(); leftWidth == 0 {
		hints = []keyHint{
			{"←/→", "source"}, {"↑/↓", "move"}, {"Enter", "open"}, {"i/m/d", "stage"},
			{"a", "apply"}, {"/", "search"}, {"h", "help"}, {"q", "quit"},
		}
	}
	keys := renderKeyHints(hints)
	if status != "" {
		keys = status + "  |  " + keys
	}
	return fitLine(keys, m.width)
}

type keyHint struct {
	key   string
	label string
}

func renderKeyHints(hints []keyHint) string {
	parts := make([]string, 0, len(hints))
	for _, hint := range hints {
		parts = append(parts, footerKeyStyle.Render(hint.key)+" "+footerTextStyle.Render(hint.label))
	}
	return strings.Join(parts, "  ")
}

func (m uiModel) helpView() string {
	contentWidth := max(1, min(68, m.width-6))
	lines := []string{
		titleStyle.Render("Keyboard shortcuts"),
		"",
		headingStyle.Render("Navigation"),
		helpLine("Tab / Shift+Tab", "Switch pane", contentWidth),
		helpLine("↑ / ↓ or j / k", "Move selection", contentWidth),
		helpLine("← / →", "Change source", contentWidth),
		helpLine("PgUp / PgDn", "Move one page", contentWidth),
		helpLine("Home / End", "Jump to first or last item", contentWidth),
		"",
		headingStyle.Render("Actions"),
		helpLine("Enter", "Open skill or toggle group", contentWidth),
		helpLine("i / m / d", "Stage implicit, manual, or disabled", contentWidth),
		helpLine("a / u / Esc", "Apply, clear all, or undo current", contentWidth),
		helpLine("/ / r / o", "Search, refresh, or open in editor", contentWidth),
		headingStyle.Render("Windows"),
		helpLine("Detail", "j/k scroll, o editor, Esc/Enter back", contentWidth),
		helpLine("Search", "Enter accept, Esc clear", contentWidth),
		helpLine("Confirm", "Enter/y apply, Esc/n cancel", contentWidth),
		mutedStyle.Render("h / Esc / q / Enter  close help"),
	}
	maxLines := max(3, m.height-4)
	if len(lines) > maxLines {
		lines = append(lines[:maxLines-1], mutedStyle.Render("h / Esc / q / Enter  close help"))
	}
	modal := helpStyle.Width(contentWidth).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

func helpLine(key, description string, width int) string {
	keyWidth := min(18, max(1, width/3))
	return footerKeyStyle.Render(padRight(key, keyWidth)) + "  " + truncateEnd(description, max(1, width-keyWidth-2))
}

func (m uiModel) confirmView() string {
	lines := []string{titleStyle.Render("Apply pending changes?"), ""}
	for _, id := range m.pendingIDs() {
		change := m.pending[id]
		line := fmt.Sprintf("  %s: %s -> %s", id, change.BaseDesired, change.Desired)
		if change.Conflict {
			line += "  CONFLICT (will not be applied)"
		}
		lines = append(lines, fitLine(line, m.width))
	}
	lines = append(lines, "", warnStyle.Render("Enter/y apply")+"  Esc/n cancel")
	return fitScreen(lines, m.width, m.height)
}

func (m uiModel) detailView() string {
	skill, ok := m.currentSkill()
	if !ok {
		return fitScreen([]string{"No skill selected", "", "Esc back"}, m.width, m.height)
	}
	presentation := m.presentationFor(skill)
	policyValue := "not set (implicit allowed)"
	if skill.Policy != nil {
		policyValue = fmt.Sprintf("%t", *skill.Policy)
	}
	lines := []string{
		titleStyle.Render(skill.Name), "",
		field("ID", skill.ID),
		field("Actual", string(skill.Actual)),
		field("Desired", string(presentation.Desired)),
		field("Status", presentation.Status),
		field("Managed", fmt.Sprintf("%t", skill.Managed)),
		field("Scope", string(skill.Scope)),
		field("Source", skill.Source),
		field("Skill path", skill.Path),
		field("Policy path", skill.PolicyPath),
		field("Policy value", policyValue),
	}
	if skill.Journal == nil {
		lines = append(lines, field("Original state", "not recorded"), field("Last sync", "never"))
	} else {
		lines = append(lines,
			field("Original state", string(originalState(skill))),
			field("Last sync", formatTime(skill.Journal.LastSyncedAt)),
		)
	}
	lines = append(lines, "", headingStyle.Render("Description"))
	lines = append(lines, wrapText(skill.Description, max(20, m.width-2))...)
	lines = append(lines, "", mutedStyle.Render("j/k scroll  o open in editor  Esc/Enter back"))
	visible := max(1, m.height)
	start := clamp(m.detailOffset, 0, max(0, len(lines)-visible))
	end := min(len(lines), start+visible)
	return fitScreen(lines[start:end], m.width, m.height)
}

func (m uiModel) layout() (leftWidth, rightWidth, mainTop, mainHeight int) {
	mainTop = 2
	mainHeight = max(3, m.height-mainTop-1)
	if m.width < 72 {
		return 0, max(1, m.width), mainTop, mainHeight
	}
	leftWidth = clamp(m.width/4, 24, 34)
	rightWidth = max(1, m.width-leftWidth-3)
	return leftWidth, rightWidth, mainTop, mainHeight
}

func (m uiModel) mainHeight() int {
	_, _, _, height := m.layout()
	return height
}

func (m uiModel) visibleRowCount() int {
	return max(1, m.mainHeight()-2)
}

func tableWidths(width int) (nameWidth, pathWidth int, showPath bool) {
	const fixed = 1 + 10 + 1 + 10 + 1 + 9 + 1 + 7
	if width < 76 {
		return max(8, width-fixed), 0, false
	}
	nameWidth = clamp(width/4, 14, 30)
	pathWidth = max(8, width-fixed-nameWidth-1)
	return nameWidth, pathWidth, true
}

func stateMarker(state model.InvocationState) string {
	switch state {
	case model.StateImplicit:
		return "●"
	case model.StateManual:
		return "◆"
	default:
		return "○"
	}
}

func renderMarker(marker string, state model.InvocationState) string {
	switch marker {
	case "×", "!":
		return errorStyle.Render(marker)
	case "~":
		return warnStyle.Render(marker)
	}
	switch state {
	case model.StateImplicit:
		return implicitStyle.Render(marker)
	case model.StateManual:
		return manualStyle.Render(marker)
	default:
		return disabledStyle.Render(marker)
	}
}

func summarizeGroups(groups []inventory.Group) inventory.Summary {
	var result inventory.Summary
	for _, group := range groups {
		result.Total += group.Summary.Total
		result.Implicit += group.Summary.Implicit
		result.Manual += group.Summary.Manual
		result.Disabled += group.Summary.Disabled
		result.Drift += group.Summary.Drift
	}
	return result
}

func originalState(skill service.SkillStatus) model.InvocationState {
	entry := skill.Journal
	if entry == nil || !entry.OriginalEnabled {
		return model.StateDisabled
	}
	if entry.OriginalPolicyPresent && !entry.OriginalPolicyValue {
		return model.StateManual
	}
	return model.StateImplicit
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.Local().Format(time.RFC3339)
}

func field(label, value string) string {
	return headingStyle.Render(padRight(label+":", 16)) + " " + value
}

func fitScreen(lines []string, width, height int) string {
	result := make([]string, 0, height)
	for _, line := range lines {
		result = append(result, fitLine(line, width))
		if len(result) == height {
			break
		}
	}
	for len(result) < height {
		result = append(result, "")
	}
	return strings.Join(result, "\n")
}

func wrapText(value string, width int) []string {
	if strings.TrimSpace(value) == "" {
		return []string{"(no description)"}
	}
	var result []string
	for _, paragraph := range strings.Split(value, "\n") {
		words := strings.Fields(paragraph)
		line := ""
		for _, word := range words {
			if line == "" {
				line = word
				continue
			}
			if lipgloss.Width(line)+1+lipgloss.Width(word) <= width {
				line += " " + word
			} else {
				result = append(result, truncateEnd(line, width))
				line = word
			}
		}
		if line != "" {
			result = append(result, truncateEnd(line, width))
		}
	}
	return result
}

func fitLine(value string, width int) string {
	return padRight(truncateEnd(value, width), width)
}

func padRight(value string, width int) string {
	if width <= 0 {
		return ""
	}
	valueWidth := lipgloss.Width(value)
	if valueWidth >= width {
		return value
	}
	return value + strings.Repeat(" ", width-valueWidth)
}

func truncateEnd(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "…")
}

func middleTruncate(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return truncateEnd(value, width)
	}
	if width < 5 {
		return truncateEnd(value, width)
	}
	runes := []rune(value)
	leftCount := (width - 1) / 2
	rightCount := width - 1 - leftCount
	left := strings.TrimSuffix(truncateEnd(string(runes), leftCount), "…")
	right := string(runes)
	for lipgloss.Width(right) > rightCount && len([]rune(right)) > 0 {
		rightRunes := []rune(right)
		right = string(rightRunes[1:])
	}
	return padRight(left+"…"+right, width)
}
