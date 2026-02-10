package terminal

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// TerminalState represents the current terminal mode.
type TerminalState int

const (
	StateCooked TerminalState = iota // Normal mode (TUI, menus, SFTP)
	StateRaw                         // Raw mode (SSH shell only)
)

// Manager manages terminal lifecycle.
//
// CRITICAL: This is the ONLY place in the codebase allowed to call:
// - term.MakeRaw()
// - term.Restore()
//
// Raw mode is ONLY used during SSH interactive shell sessions.
// TUI, SFTP shell, and all other interactions use cooked mode.
type Manager struct {
	mu            sync.Mutex
	originalState *term.State
	inRawMode     bool
	session       *ssh.Session
	stopResize    chan struct{}
}

// New creates a new terminal manager and saves the original terminal state.
func New() *Manager {
	m := &Manager{
		inRawMode:  false,
		stopResize: make(chan struct{}),
	}

	// Save original terminal state immediately when creating the manager
	fd := int(os.Stdin.Fd())
	state, err := term.GetState(fd)
	if err == nil {
		m.originalState = state
	}

	return m
}

// Cleanup restores terminal.
// Call this when shutting down the application.
func (m *Manager) Cleanup() {
	m.Restore()
}

// EnterRaw switches terminal to raw mode for SSH session.
//
// CRITICAL: ONLY call this when entering SSH interactive shell.
// MUST be paired with Restore() via defer.
//
// This function:
// 1. Saves the original terminal state
// 2. Switches to raw mode
// 3. Starts listening for window resize events
//
// Usage:
//
//	session, _ := client.Session()
//	if err := termMgr.EnterRaw(session); err != nil {
//	    return err
//	}
//	defer termMgr.Restore()
func (m *Manager) EnterRaw(session *ssh.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.inRawMode {
		return fmt.Errorf("already in raw mode")
	}

	// Save original terminal state (if not already saved)
	if m.originalState == nil {
		fd := int(os.Stdin.Fd())
		state, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("make raw: %w", err)
		}
		m.originalState = state
	} else {
		// Already have state, just switch to raw mode
		// Don't overwrite originalState - we want to restore to the true original
		fd := int(os.Stdin.Fd())
		_, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("make raw: %w", err)
		}
	}

	m.inRawMode = true
	m.session = session
	m.stopResize = make(chan struct{})

	// Send initial window size to remote session
	// Note: updateWindowSize has timeout protection, but session.WindowChange()
	// may still hang due to SSH library bug (https://github.com/golang/go/issues/69484)
	// We call it in a goroutine to avoid blocking EnterRaw()
	go func() {
		m.updateWindowSize()
	}()

	// Start window resize handler
	go m.handleWinch()

	return nil
}

// Restore restores the terminal to cooked mode.
//
// Safe to call multiple times (idempotent).
// Stops the window resize handler but doesn't wait for it to finish.
func (m *Manager) Restore() error {
	m.mu.Lock()

	if !m.inRawMode {
		m.mu.Unlock()
		return nil // Idempotent - already restored
	}

	// Mark as not in raw mode FIRST
	// This prevents updateWindowSize from trying to use the session
	m.inRawMode = false

	// Save reference to stop channel before clearing
	stopCh := m.stopResize

	// Clear session and create new channel for next EnterRaw
	m.session = nil
	m.stopResize = make(chan struct{})

	// Restore terminal using the original state (while holding lock)
	fd := int(os.Stdin.Fd())
	if m.originalState != nil {
		if err := term.Restore(fd, m.originalState); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("restore terminal: %w", err)
		}
	} else {
	}

	m.mu.Unlock()

	// Close the stop channel AFTER unlocking to signal goroutine to exit
	// This prevents deadlock because goroutine needs the lock to call updateWindowSize
	close(stopCh)

	// DON'T wait for goroutine - let it exit on its own in the next select iteration
	// This prevents Restore() from blocking

	return nil
}

// InRaw returns true if currently in raw mode.
func (m *Manager) InRaw() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inRawMode
}

// State returns the current terminal state.
func (m *Manager) State() TerminalState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inRawMode {
		return StateRaw
	}
	return StateCooked
}
