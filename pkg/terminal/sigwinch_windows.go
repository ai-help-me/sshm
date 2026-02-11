//go:build windows
// +build windows

package terminal

import (
	"os"
	"time"

	"golang.org/x/term"
)

// handleWinch is a no-op on Windows since SIGWINCH is not available.
// Window resize handling on Windows would require different mechanisms
// (like console API events), which are not implemented in this SSH client.
func (m *Manager) handleWinch() {
	// No-op on Windows
}

// updateWindowSize gets the current terminal size and sends it to the SSH session.
// This can still be called manually on Windows, but won't be triggered by signals.
func (m *Manager) updateWindowSize() {
	m.mu.Lock()
	if !m.inRawMode || m.session == nil {
		m.mu.Unlock()
		return
	}

	// Capture session reference under lock
	session := m.session
	m.mu.Unlock()

	// Get terminal size outside the lock
	width, height, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return
	}

	// Send window change with timeout protection
	// WindowChange can block if the session is closing
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Check again under lock to ensure we're still in raw mode
		m.mu.Lock()
		inRaw := m.inRawMode
		// Verify session hasn't changed
		if m.session != session {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		if !inRaw {
			return
		}

		// Call WindowChange outside the lock to avoid blocking
		session.WindowChange(height, width)
	}()

	// Wait with short timeout to prevent blocking
	select {
	case <-done:
		// Success
	case <-time.After(100 * time.Millisecond):
		// Timeout - WindowChange is taking too long, skip this update
	}
}
