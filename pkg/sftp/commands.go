package sftp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/schollz/progressbar/v3"
)

// formatBytes formats byte size to human readable string
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
		TB = 1024 * GB
	)

	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", float64(bytes)/float64(TB))
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// Shell implements interactive SFTP shell.
type Shell struct {
	user   string
	host   string
	client *sftp.Client
	paths  *PathState
	stdout io.Writer
	stderr io.Writer
}

// NewShell creates SFTP shell (always in cooked mode).
func NewShell(client *sftp.Client, paths *PathState, user, host string) *Shell {
	return &Shell{
		client: client,
		paths:  paths,
		stdout: os.Stdout,
		user:   user,
		host:   host,
		stderr: os.Stderr,
	}
}

// Run starts the interactive shell.
// Runs in cooked mode - uses terminal Manager for context.
func (s *Shell) Run() error {
	fmt.Fprintf(s.stdout, "SFTP shell started. Type 'help' for commands.\n")
	fmt.Fprintf(s.stdout, "Press Ctrl+C to interrupt file transfers.\n")

	// Set up signal handler for SIGINT (Ctrl+C)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	defer signal.Stop(sigChan)

	// ONE goroutine reads stdin for the entire shell lifetime
	// Use buffered channel to prevent blocking
	lineChan := make(chan string, 1)
	eofChan := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			lineChan <- line
		}
		if err := scanner.Err(); err != nil {
			eofChan <- err
		} else {
			eofChan <- io.EOF
		}
	}()

	loopCount := 0
	for {
		loopCount++
		s.showPrompt()
		select {
		case line := <-lineChan:
			input := strings.TrimSpace(line)
			if input == "" {
				continue
			}

			// Check if this is a transfer command
			parts := strings.Fields(input)
			if len(parts) == 0 {
				continue
			}
			cmd := strings.ToLower(parts[0])
			isTransfer := cmd == "get" || cmd == "put"

			if isTransfer {
				s.runTransfer(input, sigChan)
			} else {
				// For non-transfer commands, execute directly
				if err := s.executeCommand(input); err != nil {
					// Check if this is an exit command
					if err.Error() == "exit" {
						return nil
					}
					fmt.Fprintf(s.stderr, "Error: %v\n", err)
				}
			}

		case <-sigChan:
			// Ctrl+C pressed (no active transfer)
			fmt.Fprintf(s.stdout, "\n")

		case err := <-eofChan:
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}
	}
}

// runTransfer executes a transfer command (get/put) with signal handling.
// The sigChan acts as a baton: ownership passes to this method during transfer.
func (s *Shell) runTransfer(input string, sigChan <-chan os.Signal) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		err := s.executeTransferCommand(ctx, input)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			if err == context.Canceled {
				fmt.Fprintf(s.stderr, "Transfer cancelled.\n")
			} else {
				fmt.Fprintf(s.stderr, "Error: %v\n", err)
			}
		}
	case <-sigChan:
		fmt.Fprintf(s.stdout, "\n^C\nTransfer cancelled.\n")
		cancel()
		<-done // wait for cleanup
	}
}

// executeTransferCommand executes a transfer command (get/put) with context.
func (s *Shell) executeTransferCommand(ctx context.Context, input string) error {
	parts := strings.Fields(strings.TrimSpace(input))
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "get":
		return s.cmdGetWithContext(ctx, args)
	case "put":
		return s.cmdPutWithContext(ctx, args)
	default:
		return fmt.Errorf("not a transfer command: %s", cmd)
	}
}

// showPrompt displays sftp> prompt.
func (s *Shell) showPrompt() {
	fmt.Fprintf(s.stdout, "\033[1;32msftp %s@%s:%s>\033[0m ", s.user, s.host, s.paths.RemoteCWD)
	// Force flush stdout to ensure prompt is visible immediately
	// Use both Sync() and explicit flush for terminal output
	if f, ok := s.stdout.(*os.File); ok {
		f.Sync()
	} else {
		// If not a file, try to flush if it's a Writer with Flush method
		if flusher, ok := s.stdout.(interface{ Flush() }); ok {
			flusher.Flush()
		}
	}
}

// executeCommand parses and runs a single SFTP command (non-transfer).
func (s *Shell) executeCommand(input string) error {
	parts := strings.Fields(strings.TrimSpace(input))
	if len(parts) == 0 {
		return nil
	}

	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "cd":
		return s.cmdCD(args)
	case "lcd":
		return s.cmdLCD(args)
	case "pwd":
		return s.cmdPWD(args)
	case "lpwd":
		return s.cmdLPWD(args)
	case "ls":
		return s.cmdLS(args)
	case "lls":
		return s.cmdLLS(args)
	case "mkdir":
		return s.cmdMkdir(args)
	case "lmkdir":
		return s.cmdLMkdir(args)
	case "exit", "quit", "bye":
		// Return a special error to signal exit
		return fmt.Errorf("exit")
	case "help", "?":
		return s.cmdHelp()
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

// cmdCD changes the remote directory.
func (s *Shell) cmdCD(args []string) error {
	path := "~"
	if len(args) > 0 {
		path = args[0]
	}

	resolved, err := s.paths.ResolveRemote(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	// Check if directory exists and is accessible
	fi, err := s.client.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", resolved)
	}

	// CRITICAL: Update RemoteCWD using RealPath
	return s.paths.UpdateRemoteCWD(resolved)
}

// cmdLCD changes the local directory.
func (s *Shell) cmdLCD(args []string) error {
	path := "~"
	if len(args) > 0 {
		path = args[0]
	}

	resolved, err := s.paths.ResolveLocal(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	// Check if directory exists
	fi, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", resolved)
	}

	return s.paths.UpdateLocalCWD(resolved)
}

// cmdPWD prints the remote working directory.
func (s *Shell) cmdPWD(args []string) error {
	fmt.Fprintf(s.stdout, "Remote working directory: %s\n", s.paths.RemoteCWD)
	return nil
}

// cmdLPWD prints the local working directory.
func (s *Shell) cmdLPWD(args []string) error {
	fmt.Fprintf(s.stdout, "Local working directory: %s\n", s.paths.LocalCWD)
	return nil
}

// cmdLS lists remote files.
func (s *Shell) cmdLS(args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	resolved, err := s.paths.ResolveRemote(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	entries, err := s.client.ReadDir(resolved)
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		modTime := entry.ModTime().Format("Jan 02 15:04")
		size := entry.Size()

		mode := entry.Mode().String()
		fmt.Fprintf(s.stdout, "%s %8d %s %s\n", mode, size, modTime, name)
	}

	return nil
}

// cmdLLS lists local files.
func (s *Shell) cmdLLS(args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	resolved, err := s.paths.ResolveLocal(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}

		info, _ := entry.Info()
		modTime := info.ModTime().Format("Jan 02 15:04")
		size := info.Size()

		mode := info.Mode().String()
		fmt.Fprintf(s.stdout, "%s %8d %s %s\n", mode, size, modTime, name)
	}

	return nil
}

// cmdGet downloads a file from remote to local.
func (s *Shell) cmdGet(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: get remote-path [local-path]")
	}

	remotePath, err := s.paths.ResolveRemote(args[0])
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}

	localPath := ""
	if len(args) > 1 {
		localPath, err = s.paths.ResolveLocal(args[1])
	} else {
		localPath, err = s.paths.ResolveLocal(filepath.Base(args[0]))
	}
	if err != nil {
		return fmt.Errorf("resolve local: %w", err)
	}

	// Check if local path is a directory, if so append the filename
	if stat, err := os.Stat(localPath); err == nil && stat.IsDir() {
		localPath = filepath.Join(localPath, filepath.Base(remotePath))
	}

	// Open remote file
	srcFile, err := s.client.Open(remotePath)
	if err != nil {
		return fmt.Errorf("open remote: %w", err)
	}
	defer srcFile.Close()

	// Get file info
	fi, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat remote: %w", err)
	}

	// Create local file
	dstFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local: %w", err)
	}
	defer dstFile.Close()

	// Create progress bar
	bar := progressbar.NewOptions64(
		fi.Size(),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription(fmt.Sprintf("Downloading %s", filepath.Base(remotePath))),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetItsString("bytes"),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
	defer bar.Close()

	// Wrap local file with progress writer that implements io.ReaderFrom
	// This enables SFTP's concurrent read optimization
	progressDst := newProgressWriterFrom(dstFile, bar)

	// Directly call ReadFrom to enable concurrent reads
	// The SFTP client will detect the ReaderFrom interface and use concurrent operations
	_, err = progressDst.ReadFrom(srcFile)
	if err != nil {
		dstFile.Close()
		os.Remove(localPath)
		return fmt.Errorf("read from: %w", err)
	}

	fmt.Fprintln(s.stdout)
	fmt.Fprintf(s.stdout, "Download complete: %s (%s)\n", remotePath, formatBytes(fi.Size()))
	return nil
}

// cmdGetWithContext downloads a file from remote to local with cancellation support.
func (s *Shell) cmdGetWithContext(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: get remote-path [local-path]")
	}

	remotePath, err := s.paths.ResolveRemote(args[0])
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}

	localPath := ""
	if len(args) > 1 {
		localPath, err = s.paths.ResolveLocal(args[1])
	} else {
		localPath, err = s.paths.ResolveLocal(filepath.Base(args[0]))
	}
	if err != nil {
		return fmt.Errorf("resolve local: %w", err)
	}

	// Check if local path is a directory, if so append the filename
	if stat, err := os.Stat(localPath); err == nil && stat.IsDir() {
		localPath = filepath.Join(localPath, filepath.Base(remotePath))
	}

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return context.Canceled
	default:
	}

	// Open remote file
	srcFile, err := s.client.Open(remotePath)
	if err != nil {
		return fmt.Errorf("open remote: %w", err)
	}
	defer srcFile.Close()

	// Get file info
	fi, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat remote: %w", err)
	}

	// Create local file
	dstFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local: %w", err)
	}
	defer func() {
		dstFile.Close()
		// Remove file if cancelled
		if ctx.Err() == context.Canceled {
			os.Remove(localPath)
		}
	}()

	// Create progress bar with throttle to reduce update overhead
	bar := progressbar.NewOptions64(
		fi.Size(),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription(fmt.Sprintf("Downloading %s", filepath.Base(remotePath))),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetItsString("bytes"),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionThrottle(100*time.Millisecond), // Throttle updates for better performance
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
	defer bar.Close()

	// Wrap writer to track progress
	progressWriter := &progressWriter{
		writer: dstFile,
		bar:    bar,
		ctx:    ctx,
	}

	// Use io.CopyBuffer with large buffer for better performance
	buf := make([]byte, 1024*1024) // 1MB buffer
	written, err := io.CopyBuffer(progressWriter, srcFile, buf)

	// Verify file size matches expected
	if written != fi.Size() {
		dstFile.Close()
		os.Remove(localPath)
		return fmt.Errorf("incomplete download: got %d bytes, expected %d bytes", written, fi.Size())
	}

	// Sync to ensure data is written to disk
	if err := dstFile.Sync(); err != nil {
		dstFile.Close()
		os.Remove(localPath)
		return fmt.Errorf("sync file: %w", err)
	}

	// Close file explicitly before returning
	if err := dstFile.Close(); err != nil {
		os.Remove(localPath)
		return fmt.Errorf("close file: %w", err)
	}

	// Ensure progress bar finishes rendering
	bar.Close()
	fmt.Fprintln(s.stdout)
	fmt.Fprintf(s.stdout, "Download complete: %s (%s)\n", remotePath, formatBytes(fi.Size()))
	return nil
}

// cmdPutWithContext uploads a file from local to remote with cancellation support.
func (s *Shell) cmdPutWithContext(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: put local-path [remote-path]")
	}

	localPath, err := s.paths.ResolveLocal(args[0])
	if err != nil {
		return fmt.Errorf("resolve local: %w", err)
	}

	remotePath := ""
	if len(args) > 1 {
		remotePath, err = s.paths.ResolveRemote(args[1])
	} else {
		remotePath, err = s.paths.ResolveRemote(filepath.Base(args[0]))
	}
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}

	// Check if remote path is a directory, if so append the filename
	if stat, err := s.client.Stat(remotePath); err == nil && stat.IsDir() {
		remotePath = filepath.Join(remotePath, filepath.Base(localPath))
	}

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return context.Canceled
	default:
	}

	// Open local file
	srcFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local: %w", err)
	}
	defer srcFile.Close()

	// Get file info
	fi, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat local: %w", err)
	}

	// Create remote file
	dstFile, err := s.client.Create(remotePath)
	if err != nil {
		return fmt.Errorf("create remote: %w", err)
	}
	fileClosed := false
	defer func() {
		if !fileClosed {
			_ = dstFile.Close()
		}
		// Remove file if cancelled
		if ctx.Err() == context.Canceled {
			s.client.Remove(remotePath)
		}
	}()

	// Create progress bar
	bar := progressbar.NewOptions64(
		fi.Size(),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription(fmt.Sprintf("Uploading %s", filepath.Base(localPath))),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetItsString("bytes"),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
	defer bar.Close()

	// Wrap reader with progress tracking - same pattern as download
	progressReader := &progressReader{
		reader: srcFile,
		bar:    bar,
		size:   fi.Size(),
	}

	// Use io.CopyBuffer with large buffer - same pattern as download
	buf := make([]byte, 1024*1024) // 1MB buffer
	written, err := io.CopyBuffer(dstFile, progressReader, buf)
	if err != nil {
		if err == context.Canceled {
			return context.Canceled
		}
		dstFile.Close()
		fileClosed = true
		s.client.Remove(remotePath)
		return fmt.Errorf("upload: %w", err)
	}

	// Verify upload completed
	if written != fi.Size() {
		dstFile.Close()
		fileClosed = true
		s.client.Remove(remotePath)
		return fmt.Errorf("incomplete upload: sent %d bytes, expected %d bytes", written, fi.Size())
	}

	// Close remote file to finalize
	if err := dstFile.Close(); err != nil {
		return fmt.Errorf("close remote file: %w", err)
	}
	fileClosed = true

	bar.Close()
	fmt.Fprintln(s.stdout)
	fmt.Fprintf(s.stdout, "Upload complete: %s (%s)\n", remotePath, formatBytes(written))
	return nil
}

// cmdMkdir creates a directory on the remote server.
func (s *Shell) cmdMkdir(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: mkdir <path>")
	}

	path := args[0]
	resolved, err := s.paths.ResolveRemote(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	err = s.client.MkdirAll(resolved)
	if err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	fmt.Fprintf(s.stdout, "Created remote directory: %s\n", resolved)
	return nil
}

// cmdLMkdir creates a directory on the local machine.
func (s *Shell) cmdLMkdir(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lmkdir <path>")
	}

	path := args[0]
	resolved, err := s.paths.ResolveLocal(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	err = os.MkdirAll(resolved, 0755)
	if err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	fmt.Fprintf(s.stdout, "Created local directory: %s\n", resolved)
	return nil
}

// ANSI color codes
const (
	colorGreenBold = "\033[1;32m"
	colorGreen     = "\033[32m"
	colorReset     = "\033[0m"
)

// cmdHelp shows help information.
func (s *Shell) cmdHelp() error {
	fmt.Fprintf(s.stdout, "%sAvailable commands:%s\n", colorGreenBold, colorReset)
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s Change remote directory\n", colorGreen, "cd", colorReset, "<path>")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s Change local directory\n", colorGreen, "lcd", colorReset, "<path>")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s Print remote working directory\n", colorGreen, "pwd", colorReset, "")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s Print local working directory\n", colorGreen, "lpwd", colorReset, "")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s List remote files\n", colorGreen, "ls", colorReset, "[path]")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s List local files\n", colorGreen, "lls", colorReset, "[path]")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s Download file\n", colorGreen, "get", colorReset, "<remote> [local]")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s Upload file\n", colorGreen, "put", colorReset, "<local> [remote]")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s Create remote directory\n", colorGreen, "mkdir", colorReset, "<path>")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s Create local directory\n", colorGreen, "lmkdir", colorReset, "<path>")
	fmt.Fprintf(s.stdout, "  %s%-4s%s %-22s Exit SFTP shell\n", colorGreen, "exit", colorReset, "")
	return nil
}
