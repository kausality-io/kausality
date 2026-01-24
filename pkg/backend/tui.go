package backend

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kausality-io/kausality/pkg/callback/v1alpha1"
)

// Colors
var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	warning   = lipgloss.AdaptiveColor{Light: "#FFA500", Dark: "#FFB347"}
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlight).
			MarginLeft(2)

	itemStyle = lipgloss.NewStyle().
			PaddingLeft(4)

	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(2).
				Foreground(highlight).
				Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(subtle).
			PaddingLeft(4).
			PaddingTop(1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFDF5")).
			Background(lipgloss.Color("#6124DF")).
			Padding(0, 1)

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(highlight).
			Padding(1, 2).
			Width(80)

	modalTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlight).
			MarginBottom(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(subtle).
			Width(20)

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA"))

	phaseDetectedStyle = lipgloss.NewStyle().
				Foreground(warning).
				Bold(true)

	phaseResolvedStyle = lipgloss.NewStyle().
				Foreground(special).
				Bold(true)
)

// View state
type viewState int

const (
	viewList viewState = iota
	viewDetail
)

// KeyMap defines the keybindings
type KeyMap struct {
	Up     key.Binding
	Down   key.Binding
	Enter  key.Binding
	Escape key.Binding
	Delete key.Binding
	Quit   key.Binding
}

// DefaultKeyMap returns the default keybindings
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "details"),
		),
		Escape: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Delete: key.NewBinding(
			key.WithKeys("d", "backspace"),
			key.WithHelp("d", "dismiss"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
	}
}

// ShortHelp returns keybindings for short help
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Delete, k.Quit}
}

// FullHelp returns keybindings for extended help
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Escape},
		{k.Delete, k.Quit},
	}
}

// DriftReportMsg is sent when a new DriftReport arrives
type DriftReportMsg struct {
	Report *v1alpha1.DriftReport
}

// Model is the bubbletea model for the backend TUI
type Model struct {
	store  *Store
	items  []*StoredReport
	cursor int
	view   viewState
	keys   KeyMap
	help   help.Model
	width  int
	height int
	addr   string
}

// NewModel creates a new TUI model
func NewModel(store *Store, addr string) Model {
	return Model{
		store:  store,
		items:  []*StoredReport{},
		cursor: 0,
		view:   viewList,
		keys:   DefaultKeyMap(),
		help:   help.New(),
		addr:   addr,
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshItems,
		m.tickRefresh(),
	)
}

// tickRefresh returns a command that triggers a refresh every second
func (m Model) tickRefresh() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

type tickMsg struct{}

func (m Model) refreshItems() tea.Msg {
	return refreshMsg{items: m.store.List()}
}

type refreshMsg struct {
	items []*StoredReport
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		return m, tea.Batch(m.refreshItems, m.tickRefresh())

	case refreshMsg:
		m.items = msg.items
		// Adjust cursor if needed
		if m.cursor >= len(m.items) && len(m.items) > 0 {
			m.cursor = len(m.items) - 1
		}
		return m, nil

	case DriftReportMsg:
		// A new report arrived, refresh
		return m, m.refreshItems
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle escape in detail view
	if m.view == viewDetail {
		if key.Matches(msg, m.keys.Escape) {
			m.view = viewList
			return m, nil
		}
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
		return m, nil

	case key.Matches(msg, m.keys.Enter):
		if len(m.items) > 0 {
			m.view = viewDetail
		}
		return m, nil

	case key.Matches(msg, m.keys.Escape):
		m.view = viewList
		return m, nil

	case key.Matches(msg, m.keys.Delete):
		if len(m.items) > 0 && m.cursor < len(m.items) {
			id := m.items[m.cursor].Report.Spec.ID
			m.store.Remove(id)
			return m, m.refreshItems
		}
	}

	return m, nil
}

// View renders the UI
func (m Model) View() string {
	if m.view == viewDetail {
		return m.viewDetailPage()
	}
	return m.viewListPage()
}

func (m Model) viewListPage() string {
	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render("Kausality Backend"))
	b.WriteString("\n")
	b.WriteString(itemStyle.Render(fmt.Sprintf("Listening on %s", m.addr)))
	b.WriteString("\n\n")

	// Items
	if len(m.items) == 0 {
		b.WriteString(itemStyle.Render("Waiting for drift reports..."))
		b.WriteString("\n")
	} else {
		for i, item := range m.items {
			cursor := "  "
			style := itemStyle
			if i == m.cursor {
				cursor = "> "
				style = selectedItemStyle
			}

			report := item.Report
			title := fmt.Sprintf("%s/%s", report.Spec.Child.Kind, report.Spec.Child.Name)
			line := fmt.Sprintf("%s%s", cursor, title)
			b.WriteString(style.Render(line))
			b.WriteString("\n")

			phase := phaseDetectedStyle.Render("DRIFT")
			if report.Spec.Phase == v1alpha1.DriftReportPhaseResolved {
				phase = phaseResolvedStyle.Render("RESOLVED")
			}
			desc := fmt.Sprintf("   %s  parent: %s/%s  by: %s",
				phase,
				report.Spec.Parent.Kind,
				report.Spec.Parent.Name,
				report.Spec.Request.User,
			)
			b.WriteString(itemStyle.Render(desc))
			b.WriteString("\n")
		}
	}

	// Status bar
	b.WriteString("\n")
	status := fmt.Sprintf("%d drift(s)", len(m.items))
	b.WriteString(statusBarStyle.Render(status))
	b.WriteString("\n")

	// Help
	b.WriteString(helpStyle.Render(m.help.View(m.keys)))

	return b.String()
}

func (m Model) viewDetailPage() string {
	if len(m.items) == 0 || m.cursor >= len(m.items) {
		return "No item selected"
	}

	item := m.items[m.cursor]
	report := item.Report

	var b strings.Builder

	// Title
	b.WriteString(modalTitleStyle.Render("Drift Details"))
	b.WriteString("\n\n")

	// Fields
	fields := []struct {
		label string
		value string
	}{
		{"ID", report.Spec.ID},
		{"Phase", string(report.Spec.Phase)},
		{"Received", item.ReceivedAt.Format(time.RFC3339)},
		{"", ""},
		{"Parent", fmt.Sprintf("%s/%s", report.Spec.Parent.Kind, report.Spec.Parent.Name)},
		{"Parent NS", report.Spec.Parent.Namespace},
		{"Parent API", report.Spec.Parent.APIVersion},
		{"", ""},
		{"Child", fmt.Sprintf("%s/%s", report.Spec.Child.Kind, report.Spec.Child.Name)},
		{"Child NS", report.Spec.Child.Namespace},
		{"Child API", report.Spec.Child.APIVersion},
		{"", ""},
		{"User", report.Spec.Request.User},
		{"Operation", report.Spec.Request.Operation},
		{"Field Manager", report.Spec.Request.FieldManager},
	}

	for _, f := range fields {
		if f.label == "" {
			b.WriteString("\n")
			continue
		}
		line := lipgloss.JoinHorizontal(
			lipgloss.Left,
			labelStyle.Render(f.label+":"),
			valueStyle.Render(f.value),
		)
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Press ESC to go back, d to dismiss"))

	return modalStyle.Render(b.String())
}
