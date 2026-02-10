package terminal

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"
)

// handleWinch listens for SIGWINCH signals and forwards window size changes to the SSH session.
//
// This runs in a goroutine started by EnterRaw() and stopped by Restore().
func (m *Manager) handleWinch() {
	sigWinch := make(chan os.Signal, 1)
	signal.Notify(sigWinch, syscall.SIGWINCH)

	for {
		select {
		case <-sigWinch:
			m.updateWindowSize()
		case <-m.stopResize:
			signal.Stop(sigWinch)
			return
		}
	}
}

// updateWindowSize gets the current terminal size and sends it to the SSH session.
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
