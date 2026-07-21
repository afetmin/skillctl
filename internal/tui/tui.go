package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"skillctl/internal/inventory"
	"skillctl/internal/model"
	"skillctl/internal/service"
	"skillctl/internal/watcher"
)

type Options struct {
	Manager service.Manager
	Project bool
}

type rowKind int

const (
	rowGroup rowKind = iota
	rowSkill
)

const (
	focusState = iota
	focusSource
	focusTable
)

const (
	sidebarSourceOptionTop = 1
)

const (
	deleteChoiceCancel = iota
	deleteChoiceConfirm
)

type tableRow struct {
	Kind     rowKind
	Group    inventory.Group
	Skill    service.SkillStatus
	GroupKey string
}

type pendingChange struct {
	Desired     model.InvocationState
	BaseActual  model.InvocationState
	BaseDesired model.InvocationState
	Conflict    bool
}

type skillCondition string

const (
	conditionSynced   skillCondition = "synced"
	conditionDrift    skillCondition = "drift"
	conditionPending  skillCondition = "pending"
	conditionConflict skillCondition = "conflict"
	conditionApplied  skillCondition = "applied"
	conditionReadOnly skillCondition = "read-only"
)

type skillPresentation struct {
	Target    model.InvocationState
	Condition skillCondition
	Marker    string
	ReadOnly  bool
}

type presentationSummary struct {
	Drift    int
	Pending  int
	Conflict int
	Applied  int
}

type uiModel struct {
	ctx     context.Context
	manager service.Manager
	project bool

	width  int
	height int
	focus  int

	items    []service.SkillStatus
	warnings []string
	groups   []inventory.Group
	states   []inventory.StateOption
	sources  []inventory.SourceOption
	rows     []tableRow

	stateIndex   int
	sourceIndex  int
	sourceOffset int
	rowIndex     int
	rowOffset    int
	collapsed    map[string]bool
	pending      map[string]pendingChange
	applied      map[string]bool

	search          textinput.Model
	searching       bool
	help            bool
	detail          bool
	detailOffset    int
	confirm         bool
	confirmOffset   int
	applying        bool
	loading         bool
	deleteConfirm   bool
	deleteChoice    int
	deleteSkill     service.SkillStatus
	deleteNextID    string
	deleteOffset    int
	selectAfterLoad string
	deleteErr       error
	deleting        bool

	spinner     spinner.Model
	status      string
	err         error
	fingerprint string
	watch       watcher.Watcher
}

type loadedMsg struct {
	items    []service.SkillStatus
	warnings []string
	err      error
}

type appliedMsg struct {
	report model.SyncReport
	err    error
}

type fingerprintMsg struct {
	value string
	err   error
}

type editorDoneMsg struct {
	err error
}

type deletedMsg struct {
	skill service.SkillStatus
	err   error
}

func Run(ctx context.Context, options Options) error {
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "Search name, description, ID, or path"
	search.CharLimit = 256
	search.Width = 48

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))

	initial := uiModel{
		ctx:       ctx,
		manager:   options.Manager,
		project:   options.Project,
		states:    inventory.StateOptions(nil, inventory.Filter{}),
		sources:   inventory.SourceOptions(nil, inventory.Filter{}),
		collapsed: map[string]bool{},
		pending:   map[string]pendingChange{},
		applied:   map[string]bool{},
		search:    search,
		spinner:   spin,
		loading:   true,
		watch: watcher.Watcher{
			ConfigPath: options.Manager.ConfigPath,
			StatePath:  options.Manager.StatePath,
			CWD:        options.Manager.CWD,
			Interval:   2 * time.Second,
		},
	}
	program := tea.NewProgram(
		initial,
		tea.WithContext(ctx),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err := program.Run()
	return err
}

func (m uiModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadCmd(), m.fingerprintCmd())
}

func (m uiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if leftWidth, _, _, _ := m.layout(); leftWidth == 0 && m.focus == focusSource {
			m.focus = focusTable
		}
		m.ensureVisible()
		m.confirmOffset = clamp(m.confirmOffset, 0, m.confirmMaxOffset())
		m.deleteOffset = clamp(m.deleteOffset, 0, m.deleteMaxOffset())
		return m, nil
	case spinner.TickMsg:
		m.spinner, _ = m.spinner.Update(msg)
		return m, m.spinner.Tick
	case loadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.status = msg.err.Error()
			return m, nil
		}
		m.err = nil
		m.applyLoaded(msg.items, msg.warnings)
		return m, nil
	case appliedMsg:
		m.applying = false
		m.loading = true
		m.confirm = false
		m.confirmOffset = 0
		if msg.err != nil {
			m.status = msg.err.Error()
			for id, change := range m.pending {
				change.Conflict = true
				m.pending[id] = change
			}
			return m, m.loadCmd()
		}
		conflicts := m.recordApplied()
		m.status = fmt.Sprintf("Applied %d changes", msg.report.Changed)
		if conflicts > 0 {
			m.status += fmt.Sprintf("; %d conflicts remain", conflicts)
		}
		return m, m.loadCmd()
	case deletedMsg:
		m.deleting = false
		if msg.err != nil {
			m.deleteErr = msg.err
			m.deleteOffset = m.deleteMaxOffset()
			m.status = "Delete: " + msg.err.Error()
			return m, nil
		}
		delete(m.pending, msg.skill.ID)
		delete(m.applied, msg.skill.ID)
		m.deleteConfirm = false
		m.selectAfterLoad = m.deleteNextID
		m.deleteNextID = ""
		m.deleteOffset = 0
		m.deleteSkill = service.SkillStatus{}
		m.deleteErr = nil
		m.loading = true
		m.status = "Deleted " + msg.skill.Name
		return m, m.loadCmd()
	case fingerprintMsg:
		commands = append(commands, m.fingerprintCmd())
		if msg.err != nil {
			m.status = "Auto-refresh: " + msg.err.Error()
			return m, tea.Batch(commands...)
		}
		if m.fingerprint == "" {
			m.fingerprint = msg.value
			return m, tea.Batch(commands...)
		}
		if msg.value != m.fingerprint && !m.loading && !m.applying && !m.deleting {
			m.fingerprint = msg.value
			m.loading = true
			commands = append(commands, m.loadCmd())
		}
		return m, tea.Batch(commands...)
	case editorDoneMsg:
		if msg.err != nil {
			m.status = "Editor: " + msg.err.Error()
		} else {
			m.status = "Editor closed"
			m.loading = true
			commands = append(commands, m.loadCmd())
		}
		return m, tea.Batch(commands...)
	case tea.MouseMsg:
		return m.handleMouse(tea.MouseEvent(msg))
	case tea.KeyMsg:
		if m.searching {
			return m.updateSearch(msg)
		}
		if m.help {
			return m.updateHelp(msg)
		}
		if msg.String() == "h" {
			m.help = true
			return m, nil
		}
		if m.deleteConfirm {
			return m.updateDeleteConfirm(msg)
		}
		if m.confirm {
			return m.updateConfirm(msg)
		}
		if m.detail {
			return m.updateDetail(msg)
		}
		return m.updateMain(msg)
	}
	return m, tea.Batch(commands...)
}

func (m uiModel) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab":
		m.moveFocus(1)
		return m, nil
	case "shift+tab":
		m.moveFocus(-1)
		return m, nil
	case "/":
		m.searching = true
		return m, m.search.Focus()
	case "r":
		if !m.loading && !m.applying {
			m.loading = true
			m.status = "Refreshing"
			return m, m.loadCmd()
		}
	case "a":
		if len(m.pending) > 0 && !m.applying {
			m.confirm = true
			m.confirmOffset = 0
		}
		return m, nil
	case "u":
		m.pending = map[string]pendingChange{}
		m.status = "Cleared pending changes"
		return m, nil
	case "esc":
		if skill, ok := m.currentSkill(); ok {
			if _, pending := m.pending[skill.ID]; pending {
				delete(m.pending, skill.ID)
				m.status = "Removed pending change for " + skill.Name
			}
		}
		return m, nil
	case "i":
		return m.stageCurrent(model.StateImplicit), nil
	case "m":
		return m.stageCurrent(model.StateManual), nil
	case "d":
		return m.stageCurrent(model.StateDisabled), nil
	case "x":
		return m.beginDelete()
	case "o":
		return m, m.openEditorCmd()
	case "enter":
		if m.focus == focusState {
			if leftWidth, _, _, _ := m.layout(); leftWidth == 0 {
				m.focus = focusTable
			} else {
				m.focus = focusSource
			}
			return m, nil
		}
		if m.focus == focusSource {
			m.focus = focusTable
			return m, nil
		}
		if row, ok := m.currentRow(); ok {
			if row.Kind == rowGroup {
				m.collapsed[row.GroupKey] = !m.collapsed[row.GroupKey]
				m.rebuildRows("")
			} else {
				m.detail = true
				m.detailOffset = 0
			}
		}
		return m, nil
	case "up", "k":
		switch m.focus {
		case focusSource:
			m.moveSource(-1)
		case focusTable:
			m.moveRow(-1)
		}
		return m, nil
	case "down", "j":
		switch m.focus {
		case focusSource:
			m.moveSource(1)
		case focusTable:
			m.moveRow(1)
		}
		return m, nil
	case "left":
		if m.focus == focusState {
			m.moveState(-1)
		} else if m.focus == focusSource {
			m.moveSource(-1)
		}
		return m, nil
	case "right":
		if m.focus == focusState {
			m.moveState(1)
		} else if m.focus == focusSource {
			m.moveSource(1)
		}
		return m, nil
	case "[":
		m.moveState(-1)
		return m, nil
	case "]":
		m.moveState(1)
		return m, nil
	case "pgup":
		m.moveRow(-m.visibleRowCount())
		return m, nil
	case "pgdown":
		m.moveRow(m.visibleRowCount())
		return m, nil
	case "home":
		switch m.focus {
		case focusState:
			m.stateIndex = 0
			m.selectState()
		case focusSource:
			m.sourceIndex = 0
			m.selectSource()
		default:
			m.rowIndex = 0
			m.ensureVisible()
		}
		return m, nil
	case "end":
		switch m.focus {
		case focusState:
			m.stateIndex = max(0, len(m.states)-1)
			m.selectState()
		case focusSource:
			m.sourceIndex = max(0, len(m.sources)-1)
			m.selectSource()
		default:
			m.rowIndex = max(0, len(m.rows)-1)
			m.ensureVisible()
		}
		return m, nil
	}
	return m, nil
}

func (m *uiModel) moveFocus(delta int) {
	order := []int{focusState, focusSource, focusTable}
	if leftWidth, _, _, _ := m.layout(); leftWidth == 0 {
		order = []int{focusState, focusTable}
	}
	current := 0
	for index, focus := range order {
		if focus == m.focus {
			current = index
			break
		}
	}
	next := (current + delta) % len(order)
	if next < 0 {
		next += len(order)
	}
	m.focus = order[next]
}

func (m uiModel) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "h", "esc", "q", "enter":
		m.help = false
	}
	return m, nil
}

func (m uiModel) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.search.Blur()
		m.search.SetValue("")
		m.rebuild("")
		return m, nil
	case "enter":
		m.searching = false
		m.search.Blur()
		return m, nil
	}
	previous := m.search.Value()
	var command tea.Cmd
	m.search, command = m.search.Update(msg)
	if m.search.Value() != previous {
		m.rebuild("")
	}
	return m, command
}

func (m uiModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		m.applying = true
		m.status = "Applying pending changes"
		return m, m.applyCmd()
	case "n", "esc", "q":
		m.confirm = false
		m.confirmOffset = 0
		return m, nil
	case "up", "k":
		m.moveConfirm(-1)
		return m, nil
	case "down", "j":
		m.moveConfirm(1)
		return m, nil
	case "pgup":
		m.moveConfirm(-m.confirmVisibleCount())
		return m, nil
	case "pgdown":
		m.moveConfirm(m.confirmVisibleCount())
		return m, nil
	case "home":
		m.confirmOffset = 0
		return m, nil
	case "end":
		m.confirmOffset = m.confirmMaxOffset()
		return m, nil
	}
	return m, nil
}

func (m uiModel) updateDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.deleting {
		if msg.String() == "esc" {
			m.status = "Deletion is already in progress"
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.closeDeleteConfirm()
		return m, nil
	case "left", "right", "tab", "shift+tab":
		if m.deleteChoice == deleteChoiceCancel {
			m.deleteChoice = deleteChoiceConfirm
		} else {
			m.deleteChoice = deleteChoiceCancel
		}
		return m, nil
	case "up", "k":
		m.moveDeleteOffset(-1)
		return m, nil
	case "down", "j":
		m.moveDeleteOffset(1)
		return m, nil
	case "pgup":
		m.moveDeleteOffset(-m.deleteVisibleCount())
		return m, nil
	case "pgdown":
		m.moveDeleteOffset(m.deleteVisibleCount())
		return m, nil
	case "home":
		m.deleteOffset = 0
		return m, nil
	case "end":
		m.deleteOffset = m.deleteMaxOffset()
		return m, nil
	case "enter":
		if m.deleteChoice == deleteChoiceCancel {
			m.closeDeleteConfirm()
			return m, nil
		}
		m.deleting = true
		m.deleteErr = nil
		m.status = "Deleting " + m.deleteSkill.Name
		return m, m.deleteCmd()
	}
	return m, nil
}

func (m *uiModel) closeDeleteConfirm() {
	m.deleteConfirm = false
	m.deleteChoice = deleteChoiceCancel
	m.deleteSkill = service.SkillStatus{}
	m.deleteNextID = ""
	m.deleteOffset = 0
	m.deleteErr = nil
}

func (m uiModel) beginDelete() (tea.Model, tea.Cmd) {
	if m.loading || m.applying || m.deleting {
		m.status = "Wait for the current operation to finish"
		return m, nil
	}
	skill, ok := m.currentSkill()
	if !ok {
		m.status = "Select a skill to delete"
		return m, nil
	}
	if reason := deleteBlockedReason(skill); reason != "" {
		m.status = reason
		return m, nil
	}
	m.deleteConfirm = true
	m.deleteChoice = deleteChoiceCancel
	m.deleteSkill = skill
	m.deleteNextID = m.adjacentSkillID()
	m.deleteOffset = 0
	m.deleteErr = nil
	return m, nil
}

func deleteBlockedReason(skill service.SkillStatus) string {
	switch skill.Scope {
	case model.ScopeUser, model.ScopeRepo:
		return ""
	case model.ScopeSystem:
		return "System skills cannot be deleted"
	case model.ScopePlugin:
		return "Plugin skills cannot be deleted"
	case model.ScopeAdmin:
		return "Admin skills cannot be deleted"
	default:
		return "Skills from this source cannot be deleted"
	}
}

func (m uiModel) adjacentSkillID() string {
	for index := m.rowIndex + 1; index < len(m.rows); index++ {
		if m.rows[index].Kind == rowSkill {
			return m.rows[index].Skill.ID
		}
	}
	for index := m.rowIndex - 1; index >= 0; index-- {
		if m.rows[index].Kind == rowSkill {
			return m.rows[index].Skill.ID
		}
	}
	return ""
}

func (m *uiModel) moveConfirm(delta int) {
	m.confirmOffset = clamp(m.confirmOffset+delta, 0, m.confirmMaxOffset())
}

func (m uiModel) confirmVisibleCount() int {
	return max(1, m.height-4)
}

func (m uiModel) confirmMaxOffset() int {
	return max(0, len(m.pending)-m.confirmVisibleCount())
}

func (m uiModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "enter":
		m.detail = false
		m.detailOffset = 0
		return m, nil
	case "o":
		return m, m.openEditorCmd()
	case "up", "k":
		m.detailOffset = max(0, m.detailOffset-1)
	case "down", "j":
		m.detailOffset++
	case "pgup":
		m.detailOffset = max(0, m.detailOffset-m.visibleRowCount())
	case "pgdown":
		m.detailOffset += m.visibleRowCount()
	}
	return m, nil
}

func (m uiModel) handleMouse(event tea.MouseEvent) (tea.Model, tea.Cmd) {
	if m.help {
		return m, nil
	}
	if m.deleteConfirm {
		return m.handleDeleteMouse(event)
	}
	if m.confirm {
		switch event.Button {
		case tea.MouseButtonWheelUp:
			m.moveConfirm(-3)
		case tea.MouseButtonWheelDown:
			m.moveConfirm(3)
		}
		return m, nil
	}
	if m.detail {
		switch event.Button {
		case tea.MouseButtonWheelUp:
			m.detailOffset = max(0, m.detailOffset-3)
		case tea.MouseButtonWheelDown:
			m.detailOffset += 3
		}
		return m, nil
	}
	if event.Action == tea.MouseActionPress && event.Button == tea.MouseButtonLeft && event.Y == m.height-1 && event.X >= 0 && event.X < deleteFooterWidth {
		return m.beginDelete()
	}
	statusY := m.statusFilterY()
	if event.Action == tea.MouseActionPress && event.Button == tea.MouseButtonLeft && event.Y == statusY {
		if index := m.stateIndexAtX(event.X); index >= 0 {
			m.focus = focusState
			m.stateIndex = index
			m.selectState()
		}
		return m, nil
	}
	leftWidth, _, mainTop, _ := m.layout()
	if event.Button == tea.MouseButtonWheelUp || event.Button == tea.MouseButtonWheelDown {
		delta := 3
		if event.Button == tea.MouseButtonWheelUp {
			delta = -3
		}
		if event.X < leftWidth {
			m.focus = focusSource
			m.moveSource(delta)
		} else {
			m.focus = focusTable
			m.moveRow(delta)
		}
		return m, nil
	}
	if event.Action != tea.MouseActionPress || event.Button != tea.MouseButtonLeft || event.Y < mainTop {
		return m, nil
	}
	localY := event.Y - mainTop
	if event.X < leftWidth {
		index := m.sourceOffset + localY - sidebarSourceOptionTop
		if index >= 0 && index < len(m.sources) {
			m.focus = focusSource
			m.sourceIndex = index
			m.selectSource()
		}
		return m, nil
	}
	index := m.rowOffset + localY - 2
	if index >= 0 && index < len(m.rows) {
		m.focus = focusTable
		m.rowIndex = index
		m.ensureVisible()
	}
	return m, nil
}

func (m uiModel) handleDeleteMouse(event tea.MouseEvent) (tea.Model, tea.Cmd) {
	if m.deleting {
		return m, nil
	}
	switch event.Button {
	case tea.MouseButtonWheelUp:
		m.moveDeleteOffset(-3)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.moveDeleteOffset(3)
		return m, nil
	}
	if event.Action != tea.MouseActionPress || event.Button != tea.MouseButtonLeft {
		return m, nil
	}
	y, cancelStart, cancelEnd, deleteStart, deleteEnd := m.deleteButtonLayout()
	if event.Y != y {
		return m, nil
	}
	if event.X >= cancelStart && event.X < cancelEnd {
		m.closeDeleteConfirm()
		return m, nil
	}
	if event.X >= deleteStart && event.X < deleteEnd {
		m.deleteChoice = deleteChoiceConfirm
		m.deleting = true
		m.deleteErr = nil
		m.status = "Deleting " + m.deleteSkill.Name
		return m, m.deleteCmd()
	}
	return m, nil
}

func (m *uiModel) moveDeleteOffset(delta int) {
	m.deleteOffset = clamp(m.deleteOffset+delta, 0, m.deleteMaxOffset())
}

func (m uiModel) statusFilterY() int {
	y := 2
	if m.searching {
		y++
	}
	return y
}

func (m uiModel) stateIndexAtX(x int) int {
	cursor := lipgloss.Width("STATUS ")
	for index, option := range m.states {
		width := lipgloss.Width(stateOptionText(option))
		if x >= cursor && x < cursor+width {
			return index
		}
		cursor += width + 1
	}
	return -1
}

func (m *uiModel) applyLoaded(items []service.SkillStatus, warnings []string) {
	previous := map[string]service.SkillStatus{}
	for _, item := range m.items {
		previous[item.ID] = item
	}
	m.items = items
	m.warnings = warnings
	current := map[string]service.SkillStatus{}
	for _, item := range items {
		current[item.ID] = item
	}
	for id, change := range m.pending {
		item, exists := current[id]
		if !exists || item.Actual != change.BaseActual || item.Desired != change.BaseDesired {
			change.Conflict = true
			m.pending[id] = change
		}
		if _, existed := previous[id]; !existed && exists {
			change.Conflict = true
			m.pending[id] = change
		}
	}
	m.reconcileApplied(items)
	preferredSkill := m.selectAfterLoad
	m.selectAfterLoad = ""
	m.rebuild(preferredSkill)
	if len(warnings) > 0 {
		m.status = fmt.Sprintf("Loaded %d skills with %d warnings", len(items), len(warnings))
	} else {
		m.status = fmt.Sprintf("Loaded %d skills", len(items))
	}
}

func (m *uiModel) rebuild(preferredSkill string) {
	selectedState := model.InvocationState("")
	if m.stateIndex >= 0 && m.stateIndex < len(m.states) {
		selectedState = m.states[m.stateIndex].State
	}
	selectedSource := inventory.SourceOption{Key: "all", Label: "All"}
	if m.sourceIndex >= 0 && m.sourceIndex < len(m.sources) {
		selectedSource = m.sources[m.sourceIndex]
	}
	filter := inventory.Filter{
		Query:     m.search.Value(),
		State:     selectedState,
		SourceKey: selectedSource.Key,
	}
	filtered := inventory.Apply(m.items, filter)
	m.groups = inventory.GroupStatuses(filtered)
	m.states = inventory.StateOptions(m.items, filter)
	m.stateIndex = stateOptionIndex(m.states, selectedState)
	if m.stateIndex < 0 {
		m.stateIndex = 0
	}
	m.sources = inventory.SourceOptions(m.items, filter)
	m.sourceIndex = optionIndex(m.sources, selectedSource.Key)
	if m.sourceIndex < 0 {
		selectedSource.Count = 0
		m.sources = append(m.sources, selectedSource)
		m.sourceIndex = len(m.sources) - 1
	}
	m.rebuildRows(preferredSkill)
	m.ensureVisible()
}

func (m *uiModel) rebuildRows(preferredSkill string) {
	if preferredSkill == "" {
		if skill, ok := m.currentSkill(); ok {
			preferredSkill = skill.ID
		}
	}
	m.rows = nil
	if len(m.sources) == 0 {
		m.rowIndex = 0
		return
	}
	selected := m.sources[min(m.sourceIndex, len(m.sources)-1)]
	groups := m.groups
	showHeaders := selected.GroupKey == ""
	for _, group := range groups {
		if showHeaders {
			m.rows = append(m.rows, tableRow{Kind: rowGroup, Group: group, GroupKey: group.Key})
			if m.collapsed[group.Key] {
				continue
			}
		}
		for _, skill := range group.Skills {
			m.rows = append(m.rows, tableRow{Kind: rowSkill, Skill: skill, GroupKey: group.Key})
		}
	}
	if preferredSkill != "" {
		for index, row := range m.rows {
			if row.Kind == rowSkill && row.Skill.ID == preferredSkill {
				m.rowIndex = index
				break
			}
		}
	}
	if m.rowIndex >= len(m.rows) {
		m.rowIndex = max(0, len(m.rows)-1)
	}
}

func (m *uiModel) selectSource() {
	m.rowIndex = 0
	m.rowOffset = 0
	m.rebuild("")
}

func (m *uiModel) moveSource(delta int) {
	if len(m.sources) == 0 {
		return
	}
	m.sourceIndex = clamp(m.sourceIndex+delta, 0, len(m.sources)-1)
	m.selectSource()
}

func (m *uiModel) selectState() {
	m.rowIndex = 0
	m.rowOffset = 0
	m.rebuild("")
}

func (m *uiModel) moveState(delta int) {
	if len(m.states) == 0 {
		return
	}
	m.stateIndex = clamp(m.stateIndex+delta, 0, len(m.states)-1)
	m.selectState()
}

func (m *uiModel) moveRow(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.rowIndex = clamp(m.rowIndex+delta, 0, len(m.rows)-1)
	m.ensureVisible()
}

func (m *uiModel) ensureVisible() {
	visibleSources := max(1, m.mainHeight()-sidebarSourceOptionTop)
	if m.sourceIndex < m.sourceOffset {
		m.sourceOffset = m.sourceIndex
	}
	if m.sourceIndex >= m.sourceOffset+visibleSources {
		m.sourceOffset = m.sourceIndex - visibleSources + 1
	}
	visibleRows := m.visibleRowCount()
	if m.rowIndex < m.rowOffset {
		m.rowOffset = m.rowIndex
	}
	if m.rowIndex >= m.rowOffset+visibleRows {
		m.rowOffset = m.rowIndex - visibleRows + 1
	}
	m.sourceOffset = max(0, m.sourceOffset)
	m.rowOffset = max(0, m.rowOffset)
}

func (m uiModel) currentRow() (tableRow, bool) {
	if m.rowIndex < 0 || m.rowIndex >= len(m.rows) {
		return tableRow{}, false
	}
	return m.rows[m.rowIndex], true
}

func (m uiModel) currentSkill() (service.SkillStatus, bool) {
	row, ok := m.currentRow()
	if !ok || row.Kind != rowSkill {
		return service.SkillStatus{}, false
	}
	return row.Skill, true
}

func (m uiModel) stageCurrent(desired model.InvocationState) uiModel {
	skill, ok := m.currentSkill()
	if !ok {
		m.status = "Select a skill first"
		return m
	}
	if !skill.Managed {
		m.status = skill.Name + " is outside the active management scope"
		return m
	}
	delete(m.applied, skill.ID)
	if skill.Actual == skill.Desired && desired == skill.Actual {
		delete(m.pending, skill.ID)
		m.status = "Pending change removed for " + skill.Name
		return m
	}
	m.pending[skill.ID] = pendingChange{
		Desired:     desired,
		BaseActual:  skill.Actual,
		BaseDesired: skill.Desired,
	}
	m.status = fmt.Sprintf("Staged %s -> %s", skill.Name, desired)
	return m
}

func (m uiModel) presentationFor(skill service.SkillStatus) skillPresentation {
	presentation := skillPresentation{
		Condition: conditionSynced,
		Marker:    stateMarker(skill.Actual),
	}
	if !skill.Managed {
		presentation.Condition = conditionReadOnly
		presentation.ReadOnly = true
		return presentation
	}
	if pending, ok := m.pending[skill.ID]; ok {
		presentation.Target = pending.Desired
		presentation.Condition = conditionPending
		presentation.Marker = "~"
		if pending.Conflict {
			presentation.Condition = conditionConflict
			presentation.Marker = "×"
		}
		return presentation
	}
	if skill.Actual != skill.Desired {
		presentation.Condition = conditionDrift
		presentation.Marker = "!"
		return presentation
	}
	if m.applied[skill.ID] {
		presentation.Condition = conditionApplied
		presentation.Marker = "✓"
	}
	return presentation
}

func (m *uiModel) reconcileApplied(items []service.SkillStatus) {
	current := make(map[string]service.SkillStatus, len(items))
	for _, item := range items {
		current[item.ID] = item
	}
	for id := range m.applied {
		item, exists := current[id]
		if !exists || item.Actual != item.Desired {
			delete(m.applied, id)
		}
	}
}

func (m *uiModel) recordApplied() int {
	if m.applied == nil {
		m.applied = map[string]bool{}
	}
	remaining := map[string]pendingChange{}
	for id, change := range m.pending {
		if change.Conflict {
			remaining[id] = change
			continue
		}
		m.applied[id] = true
	}
	m.pending = remaining
	return len(remaining)
}

func (m uiModel) summarizePresentations() presentationSummary {
	var summary presentationSummary
	for _, skill := range m.items {
		switch m.presentationFor(skill).Condition {
		case conditionDrift:
			summary.Drift++
		case conditionPending:
			summary.Pending++
		case conditionConflict:
			summary.Conflict++
		case conditionApplied:
			summary.Applied++
		}
	}
	return summary
}

func (m uiModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		items, warnings, err := m.manager.List(m.ctx, m.project)
		return loadedMsg{items: items, warnings: warnings, err: err}
	}
}

func (m uiModel) applyCmd() tea.Cmd {
	changes := make(map[string]model.InvocationState, len(m.pending))
	for id, change := range m.pending {
		if change.Conflict {
			continue
		}
		changes[id] = change.Desired
	}
	return func() tea.Msg {
		if len(changes) == 0 {
			return appliedMsg{err: fmt.Errorf("all pending changes are conflicted")}
		}
		_, report, err := m.manager.SetMany(m.ctx, changes, false, m.project)
		if report == nil {
			return appliedMsg{err: err}
		}
		return appliedMsg{report: *report, err: err}
	}
}

func (m uiModel) deleteCmd() tea.Cmd {
	skill := m.deleteSkill
	return func() tea.Msg {
		err := m.manager.DeleteSkill(skill.Skill)
		return deletedMsg{skill: skill, err: err}
	}
}

func (m uiModel) fingerprintCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		value, err := m.watch.Fingerprint()
		return fingerprintMsg{value: value, err: err}
	})
}

func (m uiModel) openEditorCmd() tea.Cmd {
	skill, ok := m.currentSkill()
	if !ok {
		return nil
	}
	editor := os.Getenv("EDITOR")
	if strings.TrimSpace(editor) == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	command := exec.Command(parts[0], append(parts[1:], skill.Path)...)
	return tea.ExecProcess(command, func(err error) tea.Msg { return editorDoneMsg{err: err} })
}

func optionIndex(options []inventory.SourceOption, key string) int {
	for index, option := range options {
		if option.Key == key {
			return index
		}
	}
	return -1
}

func stateOptionIndex(options []inventory.StateOption, state model.InvocationState) int {
	for index, option := range options {
		if option.State == state {
			return index
		}
	}
	return -1
}

func (m uiModel) pendingIDs() []string {
	ids := make([]string, 0, len(m.pending))
	for id := range m.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
