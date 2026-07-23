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
	focusedStateStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("33"))
	footerKeyStyle           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("228"))
	footerTextStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	disabledFooterStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	dangerStyle              = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	selectedDangerStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("160"))
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
	deleteFooterWidth         = 8
)

func (m uiModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "Loading skillctl..."
	}
	if m.help {
		return m.helpView()
	}
	if m.switchConfirm {
		return m.switchConfirmView()
	}
	if m.deleteConfirm {
		return m.deleteConfirmView()
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
	if m.searching {
		lines = append(lines, m.searchInputView())
	}
	lines = append(lines, m.statusFilterView())
	right := m.tableView(rightWidth, mainHeight)
	if leftWidth == 0 {
		lines = append(lines, right...)
	} else {
		left := m.filterView(leftWidth, mainHeight)
		for index := 0; index < mainHeight; index++ {
			lines = append(lines, fitLine(left[index], leftWidth)+mutedStyle.Render(" │ ")+fitLine(right[index], rightWidth))
		}
	}
	lines = append(lines, m.footerView())
	return strings.Join(lines, "\n")
}

func (m uiModel) headerView() string {
	summary := summarizeGroups(m.groups)
	counts := fmt.Sprintf("%d skills · implicit %d", summary.Total, summary.Implicit)
	if m.manager.Agent == model.AgentClaude {
		counts += fmt.Sprintf(" · name-only %d", summary.NameOnly)
	}
	counts += fmt.Sprintf(" · manual %d · disabled %d", summary.Manual, summary.Disabled)
	line := titleStyle.Render("skillctl") + "  " + m.agentControlView() + "  " + mutedStyle.Render(counts)
	states := m.summarizePresentations()
	var badges []string
	if !m.discovery.Complete() {
		badges = append(badges, warnStyle.Render("! state verification incomplete"))
	}
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

func (m uiModel) agentControlView() string {
	parts := []string{headingStyle.Render("AGENT")}
	for index, candidate := range m.agents {
		label := " " + agentLabel(candidate) + " "
		if candidate == m.manager.Agent {
			if m.focus == focusAgent {
				label = focusedStateStyle.Render(label)
			} else {
				label = selectedStyle.Render(label)
			}
		} else if index != m.agentIndex {
			label = mutedStyle.Render(label)
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " ")
}

func (m uiModel) searchView() string {
	query := m.search.Value()
	if query == "" {
		query = "none"
	}
	line := mutedStyle.Render("Source: ") + m.selectedSourceLabel() + mutedStyle.Render(" · Search: ") + query
	return fitLine(line, m.width)
}

func (m uiModel) searchInputView() string {
	input := m.search
	input.Width = max(10, m.width-4)
	return fitLine(input.View(), m.width)
}

func (m uiModel) selectedStateLabel() string {
	if m.stateIndex >= 0 && m.stateIndex < len(m.states) {
		return m.states[m.stateIndex].Label
	}
	return "All"
}

func (m uiModel) selectedSourceLabel() string {
	if m.sourceIndex >= 0 && m.sourceIndex < len(m.sources) {
		return m.sources[m.sourceIndex].Label
	}
	return "All"
}

func (m uiModel) statusFilterView() string {
	parts := []string{headingStyle.Render("STATUS")}
	for index, option := range m.states {
		text := stateOptionText(option)
		if index == m.stateIndex {
			if m.focus == focusState {
				text = focusedStateStyle.Render(text)
			} else {
				text = selectedStyle.Render(text)
			}
		}
		parts = append(parts, text)
	}
	return fitLine(strings.Join(parts, " "), m.width)
}

func stateOptionText(option inventory.StateOption) string {
	return fmt.Sprintf(" %s %d ", option.Label, option.Count)
}

func (m uiModel) filterView(width, height int) []string {
	lines := []string{headingStyle.Render("SOURCES")}
	available := max(0, height-sidebarSourceOptionTop)
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
		if m.focus == focusSource && index == m.sourceIndex {
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
	label := m.selectedSourceLabel()
	if state := m.selectedStateLabel(); state != "All" {
		label = state + " · " + label
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
			line = fmt.Sprintf("%s %s / %s  %s", indicator, inventory.CategoryTitle(row.Group.Category), row.Group.Label, inventory.SummaryLine(row.Group.Summary, m.manager.Agent))
			line = groupStyle.Render(fitLine(line, width))
		} else {
			line = m.skillLine(row.Skill, width, m.focus == focusTable && index == m.rowIndex)
		}
		if row.Kind == rowGroup && m.focus == focusTable && index == m.rowIndex {
			line = selectedStyle.Render(fitLine(ansi.Strip(line), width))
		}
		lines = append(lines, line)
	}
	if len(m.rows) == 0 && len(lines) < height {
		lines = append(lines, mutedStyle.Render(fitLine("No skills match the current search and filters", width)))
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
	stageKeys := "i/m/d"
	if m.manager.Agent == model.AgentClaude {
		stageKeys = "i/n/m/d"
	}
	hints := []keyHint{
		{"c", "agent"}, {"Tab", "pane"}, {"↑/↓", "move"}, {"Enter", "open"}, {stageKeys, "stage"},
		{"a", "apply"}, {"/", "search"}, {"h", "help"}, {"q", "quit"},
	}
	if leftWidth, _, _, _ := m.layout(); leftWidth == 0 {
		hints = []keyHint{
			{"c", "agent"}, {"[/]", "status"}, {"Tab", "pane"}, {"↑/↓", "move"}, {"Enter", "open"},
			{"a", "apply"}, {"/", "search"}, {"h", "help"}, {"q", "quit"},
		}
	}
	deleteHint := disabledFooterStyle.Render("x Delete")
	if skill, ok := m.currentSkill(); ok && deleteBlockedReason(skill) == "" && !m.loading && !m.applying && !m.deleting {
		deleteHint = footerKeyStyle.Render("x") + " " + footerTextStyle.Render("Delete")
	}
	if lipgloss.Width(deleteHint) >= m.width {
		return truncateEnd(deleteHint, m.width)
	}
	leftReserve := 0
	if status != "" {
		leftReserve = min(lipgloss.Width(status), m.width/3) + 2
	}
	keys := renderKeyHints(hints)
	available := max(0, m.width-leftReserve-lipgloss.Width(deleteHint)-2)
	keys = truncateEnd(keys, available)
	if keys != "" {
		keys += "  "
	}
	keys += deleteHint
	return alignRight(status, keys, m.width)
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

func alignRight(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	rightWidth := lipgloss.Width(right)
	if rightWidth >= width {
		return truncateEnd(right, width)
	}
	left = truncateEnd(left, max(0, width-rightWidth-2))
	gap := width - lipgloss.Width(left) - rightWidth
	return left + strings.Repeat(" ", gap) + right
}

func (m uiModel) helpView() string {
	contentWidth := max(1, min(68, m.width-6))
	stageKeys := "i / m / d"
	stageDescription := "Stage implicit, manual, or disabled"
	if m.manager.Agent == model.AgentClaude {
		stageKeys = "i / n / m / d"
		stageDescription = "Stage implicit, name-only, manual, or disabled"
	}
	lines := []string{
		titleStyle.Render("Keyboard shortcuts"),
		"",
		headingStyle.Render("Navigation"),
		helpLine("c or Agent ← / →", "Switch the complete Agent context", contentWidth),
		helpLine("Tab / Shift+Tab", "Switch pane", contentWidth),
		helpLine("↑ / ↓ or j / k", "Move selection", contentWidth),
		helpLine("[ / ]", "Change status filter", contentWidth),
		helpLine("← / →", "Change focused status or source", contentWidth),
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
		helpLine(stageKeys, stageDescription, contentWidth),
		helpLine("a / u / Esc", "Apply, clear all, or undo current", contentWidth),
		helpLine("x / r / o", "Delete, refresh, or open in editor", contentWidth),
		helpLine("/", "Search skills", contentWidth),
		headingStyle.Render("Windows"),
		helpLine("Detail", "j/k scroll, o editor, Esc/Enter back", contentWidth),
		helpLine("Search", "Enter accept, Esc clear", contentWidth),
		helpLine("Confirm", "j/k or PgUp/PgDn scroll, Enter/y apply", contentWidth),
		mutedStyle.Render("h / Esc / q / Enter  close help"),
	}
	maxLines := max(3, m.height-4)
	if len(lines) > maxLines {
		lines = append(lines[:maxLines-1], mutedStyle.Render("h / Esc / q / Enter  close help"))
	}
	modal := helpStyle.Width(contentWidth).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

func (m uiModel) deleteConfirmView() string {
	contentWidth := m.deleteContentWidth()
	content := m.deleteConfirmLines()
	visible := m.deleteVisibleCount()
	start := clamp(m.deleteOffset, 0, max(0, len(content)-visible))
	end := min(len(content), start+visible)
	lines := append([]string(nil), content[start:end]...)
	cancel := "[ Cancel ]"
	deleteButton := dangerStyle.Render("[ Delete ]")
	if m.deleteChoice == deleteChoiceCancel {
		cancel = selectedStyle.Render(cancel)
	} else {
		deleteButton = selectedDangerStyle.Render("[ Delete ]")
	}
	cancelStart, _, _, _ := deleteButtonRanges(contentWidth)
	buttons := strings.Repeat(" ", cancelStart) + cancel + "    " + deleteButton
	lines = append(lines, fitLine(buttons, contentWidth))
	hint := mutedStyle.Render("↑/↓ scroll · ←/→ or Tab choose · Enter confirm · Esc cancel")
	if m.deleting {
		hint = m.spinner.View() + " deleting " + m.deleteSkill.Name
	}
	lines = append(lines, fitLine(hint, contentWidth))
	modal := helpStyle.Width(contentWidth).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

func (m uiModel) deleteConfirmLines() []string {
	contentWidth := m.deleteContentWidth()
	lines := []string{
		titleStyle.Render("Delete skill?"),
		"",
		field("Name", m.deleteSkill.Name),
	}
	lines = append(lines, wrapFullText("Path: "+m.deleteSkill.Path, contentWidth)...)
	if _, pending := m.pending[m.deleteSkill.ID]; pending {
		lines = append(lines, "", warnStyle.Render("This skill has a pending change; it will be cleared after deletion."))
	}
	if m.deleteErr != nil {
		lines = append(lines, "", errorStyle.Render("Delete failed: "+m.deleteErr.Error()))
	}
	return lines
}

func (m uiModel) deleteContentWidth() int {
	frameWidth, _ := helpStyle.GetFrameSize()
	return max(1, min(76, m.width-frameWidth))
}

func (m uiModel) deleteVisibleCount() int {
	_, frameHeight := helpStyle.GetFrameSize()
	available := max(1, m.height-frameHeight-2)
	return min(len(m.deleteConfirmLines()), available)
}

func (m uiModel) deleteMaxOffset() int {
	return max(0, len(m.deleteConfirmLines())-m.deleteVisibleCount())
}

func (m uiModel) deleteButtonLayout() (y, cancelStart, cancelEnd, deleteStart, deleteEnd int) {
	contentWidth := m.deleteContentWidth()
	frameWidth, frameHeight := helpStyle.GetFrameSize()
	modalWidth := contentWidth + frameWidth
	modalHeight := m.deleteVisibleCount() + 2 + frameHeight
	originX := max(0, (m.width-modalWidth)/2)
	originY := max(0, (m.height-modalHeight)/2)
	contentX := originX + helpStyle.GetBorderLeftSize() + helpStyle.GetPaddingLeft()
	contentY := originY + helpStyle.GetBorderTopSize() + helpStyle.GetPaddingTop()
	cancelStart, cancelEnd, deleteStart, deleteEnd = deleteButtonRanges(contentWidth)
	return contentY + m.deleteVisibleCount(), contentX + cancelStart, contentX + cancelEnd, contentX + deleteStart, contentX + deleteEnd
}

func deleteButtonRanges(width int) (cancelStart, cancelEnd, deleteStart, deleteEnd int) {
	cancelWidth := lipgloss.Width("[ Cancel ]")
	deleteWidth := lipgloss.Width("[ Delete ]")
	total := cancelWidth + 4 + deleteWidth
	start := max(0, (width-total)/2)
	return start, start + cancelWidth, start + cancelWidth + 4, start + total
}

// wrapFullText 强制换行但不截断内容，用于必须完整展示的路径。
func wrapFullText(value string, width int) []string {
	width = max(1, width)
	var lines []string
	var line strings.Builder
	lineWidth := 0
	for _, char := range value {
		charWidth := lipgloss.Width(string(char))
		if lineWidth > 0 && lineWidth+charWidth > width {
			lines = append(lines, line.String())
			line.Reset()
			lineWidth = 0
		}
		line.WriteRune(char)
		lineWidth += charWidth
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}

func helpLine(key, description string, width int) string {
	keyWidth := min(18, max(1, width/3))
	return footerKeyStyle.Render(padRight(key, keyWidth)) + "  " + truncateEnd(description, max(1, width-keyWidth-2))
}

func (m uiModel) confirmView() string {
	lines := []string{titleStyle.Render("Apply pending changes?"), ""}
	ids := m.pendingIDs()
	visible := m.confirmVisibleCount()
	start := clamp(m.confirmOffset, 0, max(0, len(ids)-visible))
	end := min(len(ids), start+visible)
	for _, id := range ids[start:end] {
		change := m.pending[id]
		line := fmt.Sprintf("  %s: %s -> %s", id, change.BaseActual, change.Desired)
		if change.Conflict {
			line += "  CONFLICT (will not be applied)"
		}
		lines = append(lines, fitLine(line, m.width))
	}
	for len(lines) < visible+2 {
		lines = append(lines, "")
	}
	position := fmt.Sprintf("%d-%d of %d", start+1, end, len(ids))
	hints := mutedStyle.Render("↑/↓ j/k PgUp/PgDn Home/End scroll") + "  " + warnStyle.Render("Enter/y apply") + "  Esc/n cancel"
	lines = append(lines, mutedStyle.Render(position), hints)
	return fitScreen(lines, m.width, m.height)
}

func (m uiModel) switchConfirmView() string {
	lines := []string{
		titleStyle.Render("Switch Agent?"),
		"",
		fmt.Sprintf("Switch %s to %s with %d pending changes.", agentLabel(m.manager.Agent), agentLabel(m.switchTarget), len(m.pending)),
		"",
	}
	choices := []string{" Cancel ", " Discard and switch ", " Apply and switch "}
	for index := range choices {
		if index == m.switchChoice {
			choices[index] = selectedStyle.Render(choices[index])
		}
	}
	lines = append(lines, strings.Join(choices, "  "), "", mutedStyle.Render("←/→ or Tab choose  Enter confirm  Esc cancel"))
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
		field("Agent", agentLabel(skill.Agent)),
		field("Condition", string(presentation.Condition)),
		field("Managed", fmt.Sprintf("%t", skill.Managed)),
		field("Scope", string(skill.Scope)),
		field("Source", skill.Source),
		field("Skill path", skill.Path),
	)
	if skill.BlockedBy != "" {
		lines = append(lines, field("Blocked by", skill.BlockedBy))
	}
	if skill.Agent == model.AgentCodex {
		lines = append(lines, field("Policy path", skill.PolicyPath), field("Policy value", policyValue))
	} else if skill.Journal != nil {
		lines = append(lines, field("Settings path", skill.Journal.OverridePath))
	}
	if skill.Journal == nil {
		lines = append(lines, field("Original state", "not recorded"), field("Last sync", "never"))
	} else {
		lines = append(lines,
			field("Original state", originalState(skill)),
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
	mainTop = 3
	if m.searching {
		mainTop++
	}
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
	case model.StateNameOnly:
		return "◐"
	case model.StateManual:
		return "◆"
	default:
		return "○"
	}
}

func agentLabel(agent model.Agent) string {
	switch agent {
	case model.AgentCodex:
		return "Codex"
	case model.AgentClaude:
		return "Claude"
	default:
		return string(agent)
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
		result.NameOnly += group.Summary.NameOnly
		result.Manual += group.Summary.Manual
		result.Disabled += group.Summary.Disabled
		result.Drift += group.Summary.Drift
	}
	return result
}

func originalState(skill service.SkillStatus) string {
	entry := skill.Journal
	if entry == nil {
		return "not recorded"
	}
	if skill.Agent == model.AgentClaude {
		if !entry.OriginalOverridePresent {
			return "inherited (override absent)"
		}
		switch entry.OriginalOverrideValue {
		case "on":
			return string(model.StateImplicit)
		case "name-only":
			return string(model.StateNameOnly)
		case "user-invocable-only":
			return string(model.StateManual)
		case "off":
			return string(model.StateDisabled)
		default:
			return "unknown (" + entry.OriginalOverrideValue + ")"
		}
	}
	if !entry.OriginalEnabled {
		return string(model.StateDisabled)
	}
	if entry.OriginalPolicyPresent && !entry.OriginalPolicyValue {
		return string(model.StateManual)
	}
	return string(model.StateImplicit)
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
