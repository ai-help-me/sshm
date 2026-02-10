package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ai-help-me/sshm/pkg/config"
	"github.com/ai-help-me/sshm/pkg/sftp"
	"github.com/ai-help-me/sshm/pkg/ssh"
	"github.com/ai-help-me/sshm/pkg/terminal"
	"github.com/ai-help-me/sshm/pkg/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// 1. Load config
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		fmt.Fprintf(os.Stderr, "Create ~/.sshm.yaml with your host configurations.\n")
		os.Exit(1)
	}

	// Check if there are any hosts
	if len(cfg.Hosts) == 0 {
		fmt.Fprintf(os.Stderr, "No hosts found in config\n")
		os.Exit(1)
	}

	// 2. Create terminal manager (saves original terminal state)
	termMgr := terminal.New()
	defer termMgr.Cleanup()

	// Add panic recovery to ensure terminal is restored
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "Panic recovered: %v\n", r)
			termMgr.Restore()
			os.Exit(1)
		}
	}()

	// 3. Run TUI (in cooked mode)
	tuiModel := tui.NewModel(cfg)
	tuiProgram := tea.NewProgram(tuiModel, tea.WithAltScreen())
	finalModel, err := tuiProgram.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}

	// CRITICAL: Reset terminal after TUI exits
	fmt.Print("\033[?25h") // Show cursor
	fmt.Print("\033[0m")   // Reset all attributes

	model, ok := finalModel.(tui.Model)
	if !ok {
		fmt.Fprintf(os.Stderr, "Failed to get final model\n")
		os.Exit(1)
	}

	// Check if user quit
	if model.Quitted || model.Selected == nil {
		return
	}

	// 4. Connect based on user selection
	host := model.Selected
	mode := model.Action

	if err := connectToHost(host, mode, termMgr); err != nil {
		fmt.Fprintf(os.Stderr, "Connection error: %v\n", err)
		os.Exit(1)
	}
}

func connectToHost(host *config.Host, mode string, termMgr *terminal.Manager) error {
	if host.Jump != nil && len(host.Jump) > 0 {
		jumpChain := ssh.NewJumpChainWithTarget(host)
		defer jumpChain.Close()

		_, err := jumpChain.Connect()
		if err != nil {
			return fmt.Errorf("jump chain: %w", err)
		}

		return runSessionWithJump(jumpChain, mode, termMgr, host)
	}

	sshClient, err := ssh.NewClient(host)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer sshClient.Close()

	if err := sshClient.Dial(); err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	return runSession(sshClient, mode, termMgr, host)
}

func runSession(client *ssh.Client, mode string, termMgr *terminal.Manager, host *config.Host) error {
	switch mode {
	case "sftp":
		return runSFTP(client, termMgr, host)
	case "ssh":
		return runSSH(client, termMgr)
	default:
		return fmt.Errorf("unknown mode: %s", mode)
	}
}

func runSessionWithJump(jumpChain *ssh.JumpChain, mode string, termMgr *terminal.Manager, host *config.Host) error {
	switch mode {
	case "sftp":
		return runSFTPWithJump(jumpChain, termMgr, host)
	case "ssh":
		return runSSHWithJump(jumpChain, termMgr)
	default:
		return fmt.Errorf("unknown mode: %s", mode)
	}
}

// runSSH starts an interactive SSH shell.
// Following sshw implementation:
// 1. Setup session with StdinPipe
// 2. Connect stdout/stderr directly
// 3. Start goroutine to copy stdin -> session stdin
// 4. Enter raw mode
// 5. session.Wait()
func runSSH(client *ssh.Client, termMgr *terminal.Manager) error {
	// 1. Create session
	session, err := client.Session()
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// 2. Request PTY
	sessionConfig := ssh.DefaultSessionConfig()
	if err := ssh.RequestPTY(session, sessionConfig); err != nil {
		session.Close()
		return fmt.Errorf("request pty: %w", err)
	}

	// 3. Get stdin pipe FIRST (before setting up IO)
	stdinPipe, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("stdin pipe: %w", err)
	}

	// 4. Connect stdout/stderr directly
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// 5. Start shell (before entering raw mode)
	if err := ssh.StartShell(session); err != nil {
		stdinPipe.Close()
		session.Close()
		return fmt.Errorf("start shell: %w", err)
	}

	// 6. Create a done channel to signal when session ends
	sessionDone := make(chan error, 1)

	// 7. Start stdin forwarding goroutine IMMEDIATELY
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		// Copy from local stdin to remote stdin
		_, _ = io.Copy(stdinPipe, os.Stdin)
		// When stdin ends, close the pipe
		stdinPipe.Close()
	}()

	// 8. Start session wait goroutine
	go func() {
		err := session.Wait()
		sessionDone <- err
	}()

	// 9. NOW enter raw mode (after goroutines are started)
	if err := termMgr.EnterRaw(session); err != nil {
		stdinPipe.Close()
		session.Close()
		return fmt.Errorf("enter raw mode: %w", err)
	}

	// 10. Wait for either session to end or stdin to close
	// Note: Normal SSH sessions will wait indefinitely until user exits or session ends.
	// We only use timeout when stdin closes but session doesn't end (indicating a problem).
	var waitErr error
	select {
	case waitErr = <-sessionDone:
		// CRITICAL: Restore terminal FIRST to break io.Copy's os.Stdin.Read() block
		// This must happen before closing stdinPipe, otherwise io.Copy stays blocked
		if restoreErr := termMgr.Restore(); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore terminal: %v\n", restoreErr)
		}
		// Now close stdinPipe - this should allow io.Copy to exit since terminal is restored
		stdinPipe.Close()
		// Don't block forever - stdin goroutine should exit now that terminal is restored
		select {
		case <-stdinDone:
		case <-time.After(100 * time.Millisecond):
		}
	case <-stdinDone:
		// Stdin closed, give session a moment to finish
		select {
		case waitErr = <-sessionDone:
		case <-time.After(500 * time.Millisecond):
			// Timeout - force close session
			session.Close()
			waitErr = <-sessionDone
		}
		// Restore terminal when stdin closes first
		if restoreErr := termMgr.Restore(); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore terminal: %v\n", restoreErr)
		}
	}

	// 11. Restore terminal (if not already restored in select branches above)
	// Note: Restore() is idempotent, so calling it again is safe
	if termMgr.InRaw() {
		if restoreErr := termMgr.Restore(); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore terminal: %v\n", restoreErr)
		}
	}

	// 12. Print newline
	fmt.Println()

	// Ignore exit errors
	_ = waitErr
	return nil
}

func runSSHWithJump(jumpChain *ssh.JumpChain, termMgr *terminal.Manager) error {
	// 1. Create session
	session, err := jumpChain.Session()
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// 2. Request PTY
	sessionConfig := ssh.DefaultSessionConfig()
	if err := ssh.RequestPTY(session, sessionConfig); err != nil {
		session.Close()
		return fmt.Errorf("request pty: %w", err)
	}

	// 3. Get stdin pipe
	stdinPipe, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("stdin pipe: %w", err)
	}

	// 4. Connect stdout/stderr
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// 5. Start shell
	if err := ssh.StartShell(session); err != nil {
		stdinPipe.Close()
		session.Close()
		return fmt.Errorf("start shell: %w", err)
	}

	// 6. Create done channel
	sessionDone := make(chan error, 1)

	// 7. Start stdin forwarding
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		_, _ = io.Copy(stdinPipe, os.Stdin)
		stdinPipe.Close()
	}()

	// 8. Start session wait goroutine
	go func() {
		sessionDone <- session.Wait()
	}()

	// 9. Enter raw mode
	if err := termMgr.EnterRaw(session); err != nil {
		stdinPipe.Close()
		session.Close()
		return fmt.Errorf("enter raw mode: %w", err)
	}

	// 10. Wait for either session or stdin
	var waitErr error
	select {
	case waitErr = <-sessionDone:
		// CRITICAL: Restore terminal FIRST to break io.Copy's os.Stdin.Read() block
		if restoreErr := termMgr.Restore(); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore terminal: %v\n", restoreErr)
		}
		stdinPipe.Close()
		select {
		case <-stdinDone:
		case <-time.After(100 * time.Millisecond):
		}
	case <-stdinDone:
		select {
		case waitErr = <-sessionDone:
		case <-time.After(500 * time.Millisecond):
			session.Close()
			waitErr = <-sessionDone
		}
		// Restore terminal when stdin closes first
		if restoreErr := termMgr.Restore(); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore terminal: %v\n", restoreErr)
		}
	}

	// 11. Restore terminal (if not already restored in select branches above)
	if !termMgr.InRaw() {
	} else {
		if restoreErr := termMgr.Restore(); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore terminal: %v\n", restoreErr)
		}
	}

	// 12. Print newline
	fmt.Println()

	_ = waitErr
	return nil
}

func runSFTP(client *ssh.Client, termMgr *terminal.Manager, host *config.Host) error {
	sshClient := client.GetSSHClient()
	if sshClient == nil {
		return fmt.Errorf("not connected")
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("create sftp client: %w", err)
	}
	defer sftpClient.Close()

	paths, err := sftp.NewPathState(sftpClient)
	if err != nil {
		return fmt.Errorf("create path state: %w", err)
	}

	// Get user and host from config
	user := host.User
	hostname := host.Host
	shell := sftp.NewShell(sftpClient, paths, user, hostname)
	if err := shell.Run(); err != nil {
		return fmt.Errorf("sftp shell: %w", err)
	}

	return nil
}

func runSFTPWithJump(jumpChain *ssh.JumpChain, termMgr *terminal.Manager, host *config.Host) error {
	sshClient := jumpChain.GetSSHClient()
	if sshClient == nil {
		return fmt.Errorf("not connected")
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("create sftp client: %w", err)
	}
	defer sftpClient.Close()

	paths, err := sftp.NewPathState(sftpClient)
	if err != nil {
		return fmt.Errorf("create path state: %w", err)
	}

	// Get user and host from config
	user := host.User
	hostname := host.Host
	shell := sftp.NewShell(sftpClient, paths, user, hostname)
	if err := shell.Run(); err != nil {
		return fmt.Errorf("sftp shell: %w", err)
	}

	return nil
}
