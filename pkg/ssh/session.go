package ssh

import (
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// SessionConfig contains PTY configuration.
type SessionConfig struct {
	Term   string
	Height int
	Width  int
	Modes  ssh.TerminalModes
}

// DefaultSessionConfig returns default PTY configuration with actual terminal size.
func DefaultSessionConfig() *SessionConfig {
	width, height := 80, 24

	// Get actual terminal size
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		if w, h, err := term.GetSize(fd); err == nil {
			width, height = w, h
		}
	}

	return &SessionConfig{
		Term:   "xterm-256color",
		Height: height,
		Width:  width,
		Modes: ssh.TerminalModes{
			ssh.ECHO:          1,     // enable echo
			ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
			ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
		},
	}
}

// RequestPTY requests a pseudo-terminal for the session.
func RequestPTY(session *ssh.Session, config *SessionConfig) error {
	if config == nil {
		config = DefaultSessionConfig()
	}

	if err := session.RequestPty(config.Term, config.Height, config.Width, config.Modes); err != nil {
		return fmt.Errorf("request pty: %w", err)
	}

	return nil
}

// SetupPipes connects session stdin/stdout/stderr to os pipes.
func SetupPipes(session *ssh.Session) {
	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
}

// StartShell starts an interactive shell on the session.
//
// IMPORTANT: Caller must use terminal.Manager.EnterRaw() before calling this
// and terminal.Manager.Restore() after the shell ends.
func StartShell(session *ssh.Session) error {
	if err := session.Shell(); err != nil {
		return fmt.Errorf("start shell: %w", err)
	}

	return nil
}

// RunCommand executes a single command on the remote host.
func RunCommand(session *ssh.Session, cmd string) error {
	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("run command: %w", err)
	}
	return nil
}

// Output runs a command and returns its output.
func Output(session *ssh.Session, cmd string) ([]byte, error) {
	output, err := session.Output(cmd)
	if err != nil {
		return nil, fmt.Errorf("command output: %w", err)
	}
	return output, nil
}

// CombinedOutput runs a command and returns its combined stdout and stderr.
func CombinedOutput(session *ssh.Session, cmd string) ([]byte, error) {
	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return nil, fmt.Errorf("command combined output: %w", err)
	}
	return output, nil
}
