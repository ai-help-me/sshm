package tui

import (
	"runtime/debug"
	"strings"

	"github.com/ai-help-me/sshm/pkg/config"
	tea "github.com/charmbracelet/bubbletea"
)

// ViewMode represents the current TUI view mode.
type ViewMode int

const (
	ModeHostList ViewMode = iota
	ModeSearching
	ModeSelectAction
)

// HostSelectedMsg is sent when a host is selected.
type HostSelectedMsg struct {
	Host *config.Host
	Mode string // "ssh" or "sftp"
}

// Model is the main Bubbletea model.
type Model struct {
	config       *config.Config
	hosts        []*config.Host
	filtered     []*config.Host
	cursor       int
	actionCursor int // For action selection mode (0=ssh, 1=sftp)
	Selected     *config.Host
	searching    bool
	query        string
	err          error
	Quitted      bool
	mode         ViewMode
	Action       string // "ssh" or "sftp"
	styles       Styles
	keys         KeyBindings
	currentPath  []string // Current navigation path (empty = root level)
	width        int      // Terminal width
	height       int      // Terminal height
}

// NewModel creates a new TUI model.
func NewModel(cfg *config.Config) Model {
	keys := DefaultKeyBindings()
	styles := DefaultStyles()

	// Start at root level
	hosts := cfg.GetHostsAtPath([]string{})

	return Model{
		config:      cfg,
		hosts:       hosts,
		filtered:    hosts,
		mode:        ModeHostList,
		styles:      styles,
		keys:        keys,
		currentPath: []string{},
		width:       80, // Default width, will be updated by WindowSizeMsg
		height:      24, // Default height, will be updated by WindowSizeMsg
	}
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	// Request initial window size
	return tea.WindowSize()
}

// Update handles messages (Elm architecture).
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.WindowSizeMsg:
		// Update terminal dimensions
		m.width = msg.Width
		m.height = msg.Height
		// Update styles with new width
		m.styles = m.styles.WithWidth(m.width)
		return m, nil

	default:
		return m, nil
	}
}

// handleKeyMsg processes keyboard input.
func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle quit
	if msg.String() == "q" || msg.String() == "ctrl+c" {
		m.Quitted = true
		return m, tea.Quit
	}

	// Handle different modes
	switch m.mode {
	case ModeHostList:
		return m.updateHostList(msg)

	case ModeSearching:
		return m.updateSearching(msg)

	case ModeSelectAction:
		return m.updateSelectAction(msg)
	}

	return m, nil
}

// updateHostList handles key messages in host list mode.
func (m Model) updateHostList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}

	case "enter":
		if len(m.filtered) > 0 {
			selected := m.filtered[m.cursor]
			// Check if it's a group (has children) or a leaf node
			if len(selected.Children) > 0 {
				// It's a group, enter it
				m.currentPath = append(m.currentPath, selected.Name)
				m.hosts = selected.Children
				m.filtered = selected.Children
				m.cursor = 0
			} else {
				// It's a leaf node, select it for connection
				m.Selected = selected
				m.mode = ModeSelectAction
			}
		}

	case "esc":
		// Go back to parent level
		if len(m.currentPath) > 0 {
			// Pop last path segment
			m.currentPath = m.currentPath[:len(m.currentPath)-1]
			m.hosts = m.config.GetHostsAtPath(m.currentPath)
			m.filtered = m.hosts
			m.cursor = 0
		}

	case "/":
		m.mode = ModeSearching
		m.searching = true
		m.query = ""
	}

	return m, nil
}

// updateSearching handles key messages in search mode.
func (m Model) updateSearching(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Exit search mode
		m.mode = ModeHostList
		m.searching = false
		m.query = ""
		m.filtered = m.hosts
		m.cursor = 0

	case "enter":
		// Select first result if any
		if len(m.filtered) > 0 {
			m.Selected = m.filtered[0]
			m.mode = ModeSelectAction
		}

	case "backspace":
		// Remove last character
		if len(m.query) > 0 {
			m.query = m.query[:len(m.query)-1]
			m.filterHosts()
		}

	default:
		// Handle character input
		if msg.Type == tea.KeyRunes {
			m.query += string(msg.Runes)
			m.filterHosts()
		}
	}

	return m, nil
}

// updateSelectAction handles key messages in action selection mode.
func (m Model) updateSelectAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.actionCursor > 0 {
			m.actionCursor--
		}

	case "down", "j":
		if m.actionCursor < 1 {
			m.actionCursor++
		}

	case "enter":
		// Select based on cursor position
		if m.actionCursor == 0 {
			m.Action = "ssh"
		} else {
			m.Action = "sftp"
		}
		return m, tea.Quit

	case "esc":
		// Return to host list
		m.mode = ModeHostList
		m.Selected = nil
		m.actionCursor = 0
	}

	return m, nil
}

// filterHosts filters the host list based on search query.
func (m *Model) filterHosts() {
	if m.query == "" {
		m.filtered = m.hosts
		m.cursor = 0
		return
	}

	query := strings.ToLower(m.query)
	m.filtered = nil

	for _, host := range m.hosts {
		if strings.Contains(strings.ToLower(host.Name), query) ||
			strings.Contains(strings.ToLower(host.Host), query) ||
			strings.Contains(strings.ToLower(host.User), query) {
			m.filtered = append(m.filtered, host)
		}
	}

	m.cursor = 0
}

// View renders the UI.
func (m Model) View() string {
	if m.Quitted {
		return ""
	}

	var b strings.Builder

	// Banner
	b.WriteString(m.renderBanner())
	b.WriteString("\n")

	switch m.mode {
	case ModeHostList, ModeSearching:
		b.WriteString(m.renderHostList())

	case ModeSelectAction:
		b.WriteString(m.renderActionSelect())
	}

	// Help
	b.WriteString("\n")
	b.WriteString(m.renderHelp())

	return b.String()
}

// renderHostList renders the host list.
func (m Model) renderHostList() string {
	var b strings.Builder

	// Show breadcrumb path if not at root
	if len(m.currentPath) > 0 {
		breadcrumb := strings.Join(m.currentPath, " / ")
		b.WriteString(m.styles.HostItemDim.Render("Path: " + breadcrumb))
		b.WriteString("\n")
	}

	if m.mode == ModeSearching {
		b.WriteString(m.styles.SearchPrompt.Render("Search: " + m.query + "_"))
		b.WriteString("\n")
	}

	if len(m.filtered) == 0 {
		b.WriteString(m.styles.HostItemDim.Render("No hosts found"))
		return b.String()
	}

	for i, host := range m.filtered {
		cursor := " "
		isSelected := i == m.cursor
		if isSelected {
			cursor = ">"
		}

		// Build host line - style differently for selected vs non-selected
		// to avoid Lipgloss style nesting issues
		var name, addr string
		isGroup := len(host.Children) > 0

		if isSelected {
			// For selected row, use plain text so cursor style (black fg, cyan bg) works
			if isGroup {
				name = "+ " + host.Name
				addr = "" // Groups don't show address
			} else {
				name = host.Name
				addr = host.User + "@" + host.Host
			}
		} else {
			// For non-selected rows, apply individual styles
			if isGroup {
				name = m.styles.HostName.Render("+ " + host.Name)
				addr = "" // Groups don't show address
			} else {
				name = m.styles.HostName.Render(host.Name)
				addr = m.styles.HostAddr.Render(
					host.User + "@" + host.Host,
				)
			}
		}

		line := cursor + " " + name
		if addr != "" {
			line += " - " + addr
		}

		if isSelected {
			b.WriteString(m.styles.HostItemCursor.Render(line))
		} else {
			b.WriteString(m.styles.HostItem.Render(line))
		}

		b.WriteString("\n")
	}

	return b.String()
}

// renderActionSelect renders the action selection prompt.
func (m Model) renderActionSelect() string {
	var b strings.Builder

	b.WriteString(m.styles.Title.Render("Selected: " + m.Selected.Name))
	b.WriteString("\n")
	b.WriteString(m.styles.ModePrompt.Render("Connect via:"))
	b.WriteString("\n")

	// SSH option
	cursor := " "
	if m.actionCursor == 0 {
		cursor = ">"
	}
	line := cursor + " SSH"
	if m.actionCursor == 0 {
		b.WriteString(m.styles.HostItemCursor.Render(line))
	} else {
		b.WriteString(m.styles.HostItem.Render(line))
	}
	b.WriteString("\n")

	// SFTP option
	cursor = " "
	if m.actionCursor == 1 {
		cursor = ">"
	}
	line = cursor + " SFTP"
	if m.actionCursor == 1 {
		b.WriteString(m.styles.HostItemCursor.Render(line))
	} else {
		b.WriteString(m.styles.HostItem.Render(line))
	}

	b.WriteString("\n")
	b.WriteString(m.styles.HostItemDim.Render("Press ESC to go back"))

	return b.String()
}

// renderBanner renders the SSHM ASCII art banner.
func (m Model) renderBanner() string {
	var b strings.Builder

	// Get version from build info
	version := "dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}

	// ASCII art for SSHM (block chars, no shadow)
	logo := `  ███████ ███████ ██   ██ ███   ███
  ██      ██      ██   ██ ████ ████
  ███████ ███████ ███████ ██ ███ ██
       ██      ██ ██   ██ ██  █  ██
  ███████ ███████ ██   ██ ██     ██`

	b.WriteString(m.styles.BannerLogo.Render(logo))
	b.WriteString("\n\n")
	b.WriteString(m.styles.BannerDesc.Render("SSH/SFTP Connection Manager"))
	b.WriteString("\n")
	b.WriteString(m.styles.BannerVersion.Render("Version: " + version))
	b.WriteString("\n")

	return b.String()
}

// renderHelp renders the help text.
func (m Model) renderHelp() string {
	var help []string

	switch m.mode {
	case ModeHostList:
		if len(m.currentPath) > 0 {
			help = []string{
				m.keys.Up + " up", m.keys.Down + " down", m.keys.Select + " select",
				"esc back", m.keys.Search + " search", m.keys.Quit + " quit",
			}
		} else {
			help = []string{
				m.keys.Up + " up", m.keys.Down + " down", m.keys.Select + " select",
				m.keys.Search + " search", m.keys.Quit + " quit",
			}
		}

	case ModeSearching:
		help = []string{
			"type to search", "enter select", "esc cancel",
		}

	case ModeSelectAction:
		help = []string{
			m.keys.Up + " up", m.keys.Down + " down", m.keys.Select + " select", "esc back",
		}
	}

	return m.styles.Help.Render(strings.Join(help, " • "))
}
