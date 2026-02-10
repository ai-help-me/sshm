package tui

// KeyBindings defines key help strings for the TUI.
type KeyBindings struct {
	Quit       string
	Up         string
	Down       string
	Select     string
	Search     string
	Cancel     string
	SSHMode    string
	SFTPMode   string
}

// DefaultKeyBindings returns the default key help strings.
func DefaultKeyBindings() KeyBindings {
	return KeyBindings{
		Quit:     "q",
		Up:       "↑/k",
		Down:     "↓/j",
		Select:   "enter",
		Search:   "/",
		Cancel:   "esc",
		SSHMode:  "s",
		SFTPMode: "f",
	}
}
