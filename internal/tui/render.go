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
	titleStyle               = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	mutedStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	headingStyle             = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	selectedStyle            = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24"))
	selectedPendingStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220")).Background(lipgloss.Color("24"))
	selectedConflictStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")).Background(lipgloss.Color("24"))
	selectedDescriptionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("24"))
	footerKeyStyle           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("228"))
	footerTextStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	helpStyle                = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("39")).Padding(1, 2)
	groupStyle               = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
	warnStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	driftStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	pendingStyle             = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	conflictStyle            = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	errorStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	successStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

const (
	stateColumnWidth          = 23
	minNameColumnWidth        = 8
	minDescriptionColumnWidth = 24
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
	line := titleStyle.Render("skillctl") + "  " + mutedStyle.Render(fmt.Sprintf("%d skills · implicit %d · manual %d · disabled %d",
		summary.Total, summary.Implicit, summary.Manual, summary.Disabled))
	states := m.summarizePresentations()
	var badges []string
	if states.Drift > 0 {
		badges = append(badges, driftStyle.Render(fmt.Sprintf("! %d drift", states.Drift)))
	}
	if states.Pending > 0 {
		badges = append(badges, pendingStyle.Render(fmt.Sprintf("~ %d pending", states.Pending)))
	}
	if states.Conflict > 0 {
		badges = append(badges, errorStyle.Render(fmt.Sprintf("× %d conflict", states.Conflict)))
	}
	if states.Applied > 0 {
		badges = append(badges, successStyle.Render(fmt.Sprintf("✓ %d applied", states.Applied)))
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
			line = m.skillLine(row.Skill, width, m.focus == 1 && index == m.rowIndex)
		}
		if row.Kind == rowGroup && m.focus == 1 && index == m.rowIndex {
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
	nameWidth, descriptionWidth := m.tableWidths(width)
	header := padRight("NAME", nameWidth) + " " + padRight("STATE", stateColumnWidth)
	if descriptionWidth > 0 {
		header += " " + padRight("DESCRIPTION", descriptionWidth)
	}
	return fitLine(header, width)
}

func (m uiModel) skillLine(skill service.SkillStatus, width int, selected bool) string {
	presentation := m.presentationFor(skill)
	nameWidth, descriptionWidth := m.tableWidths(width)
	nameWidth = max(3, nameWidth)
	nameText := presentation.Marker + " " + padRight(truncateEnd(skill.Name, nameWidth-2), nameWidth-2)
	description := padRight(truncateEnd(normalizeDescription(skill.Description), descriptionWidth), descriptionWidth)
	if selected {
		line := selectedStyle.Render(nameText) + selectedStyle.Render(" ") + renderSelectedState(skill.Actual, presentation, stateColumnWidth)
		if descriptionWidth > 0 {
			line += selectedStyle.Render(" ") + selectedDescriptionStyle.Render(description)
		}
		return line
	}
	name := renderMarker(presentation.Marker) + " " + padRight(truncateEnd(skill.Name, nameWidth-2), nameWidth-2)
	line := name + " " + padRight(renderState(skill.Actual, presentation), stateColumnWidth)
	if descriptionWidth > 0 {
		line += " " + mutedStyle.Render(description)
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
		headingStyle.Render("Status"),
		helpLine("! / ~", "Drift / pending change", contentWidth),
		helpLine("× / ✓", "Conflict / applied this session", contentWidth),
		helpLine("· read-only", "Outside active management scope", contentWidth),
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
		line := fmt.Sprintf("  %s: %s -> %s", id, change.BaseActual, change.Desired)
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
		field("Current", string(skill.Actual)),
	}
	if presentation.Target != "" {
		lines = append(lines, field("Target", string(presentation.Target)))
	}
	lines = append(lines,
		field("Condition", string(presentation.Condition)),
		field("Managed", fmt.Sprintf("%t", skill.Managed)),
		field("Scope", string(skill.Scope)),
		field("Source", skill.Source),
		field("Skill path", skill.Path),
		field("Policy path", skill.PolicyPath),
		field("Policy value", policyValue),
	)
	if skill.Journal == nil {
		lines = append(lines, field("Original state", "not recorded"), field("Last sync", "never"))
	} else {
		lines = append(lines,
			field("Original state", string(originalState(skill))),
			field("Last sync", formatTime(skill.Journal.LastSyncedAt)),
		)
	}
	lines = append(lines, "", headingStyle.Render("Description"))
	description := strings.TrimSpace(skill.Description)
	if description == "" {
		description = "No description"
	}
	lines = append(lines, wrapText(description, max(20, m.width-2))...)
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

func (m uiModel) tableWidths(width int) (nameWidth, descriptionWidth int) {
	nameBudget := max(minNameColumnWidth, width-stateColumnWidth-1)
	desiredNameWidth := lipgloss.Width("NAME")
	for _, row := range m.rows {
		if row.Kind == rowSkill {
			desiredNameWidth = max(desiredNameWidth, lipgloss.Width(row.Skill.Name)+2)
		}
	}
	nameWidth = min(desiredNameWidth, nameBudget)
	descriptionWidth = width - nameWidth - stateColumnWidth - 2
	if descriptionWidth < minDescriptionColumnWidth {
		nameWidth = max(minNameColumnWidth, nameWidth-(minDescriptionColumnWidth-descriptionWidth))
		descriptionWidth = width - nameWidth - stateColumnWidth - 2
	}
	if descriptionWidth <= 0 {
		return nameBudget, 0
	}
	return nameWidth, descriptionWidth
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

func renderMarker(marker string) string {
	switch marker {
	case "×":
		return conflictStyle.Render(marker)
	case "!":
		return driftStyle.Render(marker)
	case "~":
		return pendingStyle.Render(marker)
	case "✓":
		return successStyle.Render(marker)
	}
	return mutedStyle.Render(marker)
}

func renderState(current model.InvocationState, presentation skillPresentation) string {
	state := string(current)
	if presentation.ReadOnly {
		return state + mutedStyle.Render(" · read-only")
	}
	if presentation.Target == "" {
		return state
	}
	target := string(presentation.Target)
	switch presentation.Condition {
	case conditionPending:
		target = pendingStyle.Render(target)
	case conditionConflict:
		target = conflictStyle.Render(target)
	}
	return state + " → " + target
}

func renderSelectedState(current model.InvocationState, presentation skillPresentation, width int) string {
	state := string(current)
	if presentation.ReadOnly {
		return selectedStyle.Render(padRight(state+" · read-only", width))
	}
	if presentation.Target == "" {
		return selectedStyle.Render(padRight(state, width))
	}
	prefix := state + " → "
	target := string(presentation.Target)
	targetStyle := selectedPendingStyle
	if presentation.Condition == conditionConflict {
		targetStyle = selectedConflictStyle
	}
	padding := strings.Repeat(" ", max(0, width-lipgloss.Width(prefix)-lipgloss.Width(target)))
	return selectedStyle.Render(prefix) + targetStyle.Render(target) + selectedStyle.Render(padding)
}

func normalizeDescription(value string) string {
	description := strings.Join(strings.Fields(value), " ")
	if description == "" {
		return "No description"
	}
	return description
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
