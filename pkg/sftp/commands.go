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

// Table column widths
const (
	cmdWidth  = 10
	argsWidth = 20
	descWidth = 35
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
		if entry.Mode().IsDir() {
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

// cmdGet downloads a file or directory from remote to local.
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

	// Check if remote path is a directory
	remoteInfo, err := s.client.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("stat remote: %w", err)
	}

	if remoteInfo.Mode().IsDir() {
		return s.downloadDirectory(context.Background(), remotePath, localPath)
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

// cmdGetWithContext downloads a file or directory from remote to local with cancellation support.
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

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return context.Canceled
	default:
	}

	// Check if remote path is a directory
	remoteInfo, err := s.client.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("stat remote: %w", err)
	}

	if remoteInfo.Mode().IsDir() {
		return s.downloadDirectory(ctx, remotePath, localPath)
	}

	// Single file download
	return s.downloadSingleFile(ctx, remotePath, localPath)
}

// downloadSingleFile downloads a single file from remote to local.
func (s *Shell) downloadSingleFile(ctx context.Context, remotePath, localPath string) error {
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
	if err != nil {
		dstFile.Close()
		os.Remove(localPath)
		return fmt.Errorf("copy file: %w", err)
	}

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

// downloadDirectory downloads a remote directory recursively to local.
func (s *Shell) downloadDirectory(ctx context.Context, remotePath, localPath string) error {
	// Get all files in the directory
	files, totalSize, err := s.getRemoteFileList(remotePath)
	if err != nil {
		return fmt.Errorf("scan remote directory: %w", err)
	}

	if len(files) == 0 {
		// Create empty directory
		if err := os.MkdirAll(localPath, 0755); err != nil {
			return fmt.Errorf("create local directory: %w", err)
		}
		fmt.Fprintf(s.stdout, "Downloaded empty directory: %s\n", remotePath)
		return nil
	}

	fmt.Fprintf(s.stdout, "\nDownloading %s (%d files, %s total)\n", remotePath, len(files), formatBytes(totalSize))

	var downloadedSize int64
	var downloadedCount int
	var failedFiles []string

	for i, file := range files {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
		}

		// Calculate progress prefix
		progressPrefix := fmt.Sprintf("[%d/%d]", i+1, len(files))

		// Download the file
		fileLocalPath := filepath.Join(localPath, file.RelPath)
		fileRemotePath := joinPath(remotePath, file.RelPath)

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(fileLocalPath), 0755); err != nil {
			fmt.Fprintf(s.stdout, "Warning: failed to create directory for %s: %v\n", file.RelPath, err)
			failedFiles = append(failedFiles, file.RelPath)
			continue
		}

		if err := s.downloadSingleFileWithPrefix(ctx, fileRemotePath, fileLocalPath, progressPrefix); err != nil {
			fmt.Fprintf(s.stdout, "Warning: failed to download %s: %v\n", file.RelPath, err)
			failedFiles = append(failedFiles, file.RelPath)
			continue
		}

		downloadedSize += file.Size
		downloadedCount++
	}

	// Report results
	if len(failedFiles) > 0 {
		fmt.Fprintf(s.stdout, "\nDownload completed with %d failures:\n", len(failedFiles))
		for _, f := range failedFiles {
			fmt.Fprintf(s.stdout, "  - %s\n", f)
		}
	}
	fmt.Fprintf(s.stdout, "Download complete: %d/%d files, %s/%s downloaded\n",
		downloadedCount, len(files), formatBytes(downloadedSize), formatBytes(totalSize))

	if len(failedFiles) > 0 {
		return fmt.Errorf("%d files failed to download", len(failedFiles))
	}
	return nil
}

// remoteFileInfo holds information about a remote file.
type remoteFileInfo struct {
	RelPath string
	Size    int64
}

// getRemoteFileList recursively lists all files in a remote directory.
func (s *Shell) getRemoteFileList(remotePath string) ([]remoteFileInfo, int64, error) {
	var files []remoteFileInfo
	var totalSize int64

	err := s.walkRemoteDir(remotePath, "", &files, &totalSize)
	if err != nil {
		return nil, 0, err
	}

	return files, totalSize, nil
}

// walkRemoteDir recursively walks a remote directory.
func (s *Shell) walkRemoteDir(basePath, relPath string, files *[]remoteFileInfo, totalSize *int64) error {
	currentPath := basePath
	if relPath != "" {
		currentPath = joinPath(basePath, relPath)
	}

	entries, err := s.client.ReadDir(currentPath)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", currentPath, err)
	}

	for _, entry := range entries {
		entryRelPath := entry.Name()
		if relPath != "" {
			entryRelPath = joinPath(relPath, entry.Name())
		}

		mode := entry.Mode()

		// Skip special files (symlinks, devices, sockets, pipes)
		if mode&os.ModeSymlink != 0 || mode&os.ModeDevice != 0 || mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0 {
			continue
		}

		// Use Mode().IsDir() for more reliable directory detection
		if mode.IsDir() {
			// Recurse into subdirectory
			if err := s.walkRemoteDir(basePath, entryRelPath, files, totalSize); err != nil {
				return err
			}
		} else if mode.IsRegular() {
			*files = append(*files, remoteFileInfo{
				RelPath: entryRelPath,
				Size:    entry.Size(),
			})
			*totalSize += entry.Size()
		}
	}

	return nil
}

// downloadSingleFileWithPrefix downloads a single file with a progress prefix.
func (s *Shell) downloadSingleFileWithPrefix(ctx context.Context, remotePath, localPath, prefix string) error {
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

	// Create progress bar with prefix
	bar := progressbar.NewOptions64(
		fi.Size(),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription(fmt.Sprintf("%s %s", prefix, filepath.Base(remotePath))),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetItsString("bytes"),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionThrottle(100*time.Millisecond),
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
	if err != nil {
		dstFile.Close()
		os.Remove(localPath)
		return fmt.Errorf("copy file: %w", err)
	}

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

	bar.Close()
	fmt.Fprintln(s.stdout)
	return nil
}

// cmdPutWithContext uploads a file or directory from local to remote with cancellation support.
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

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return context.Canceled
	default:
	}

	// Check if local path is a directory
	localInfo, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("stat local: %w", err)
	}

	if localInfo.IsDir() {
		return s.uploadDirectory(ctx, localPath, remotePath)
	}

	// Single file upload
	return s.uploadSingleFile(ctx, localPath, remotePath)
}

// uploadSingleFile uploads a single file from local to remote.
func (s *Shell) uploadSingleFile(ctx context.Context, localPath, remotePath string) error {
	// Check if remote path is a directory, if so append the filename
	if stat, err := s.client.Stat(remotePath); err == nil && stat.Mode().IsDir() {
		remotePath = joinPath(remotePath, filepath.Base(localPath))
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

// uploadDirectory uploads a local directory recursively to remote.
func (s *Shell) uploadDirectory(ctx context.Context, localPath, remotePath string) error {
	// Get all files in the directory
	files, totalSize, err := s.getLocalFileList(localPath)
	if err != nil {
		return fmt.Errorf("scan local directory: %w", err)
	}

	// Check if remote path exists and what type it is
	if stat, err := s.client.Stat(remotePath); err == nil {
		if !stat.Mode().IsDir() {
			return fmt.Errorf("remote path '%s' already exists and is not a directory (it's a %s)", remotePath, stat.Mode())
		}
		// Directory exists, we'll upload into it
	} else {
		// Path doesn't exist, create it
		if err := s.client.MkdirAll(remotePath); err != nil {
			return fmt.Errorf("create remote directory '%s': %w", remotePath, err)
		}
	}

	if len(files) == 0 {
		fmt.Fprintf(s.stdout, "Uploaded empty directory: %s\n", remotePath)
		return nil
	}

	fmt.Fprintf(s.stdout, "\nUploading %s (%d files, %s total)\n", localPath, len(files), formatBytes(totalSize))

	var uploadedSize int64
	var uploadedCount int
	var failedFiles []string

	for i, file := range files {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
		}

		// Calculate progress prefix
		progressPrefix := fmt.Sprintf("[%d/%d]", i+1, len(files))

		// Upload the file
		fileLocalPath := filepath.Join(localPath, file.RelPath)
		fileRemotePath := joinPath(remotePath, file.RelPath)

		// Create parent directories
		if err := s.client.MkdirAll(filepath.Dir(fileRemotePath)); err != nil {
			fmt.Fprintf(s.stdout, "Warning: failed to create directory for %s: %v\n", file.RelPath, err)
			failedFiles = append(failedFiles, file.RelPath)
			continue
		}

		if err := s.uploadSingleFileWithPrefix(ctx, fileLocalPath, fileRemotePath, progressPrefix); err != nil {
			fmt.Fprintf(s.stdout, "Warning: failed to upload %s: %v\n", file.RelPath, err)
			failedFiles = append(failedFiles, file.RelPath)
			continue
		}

		uploadedSize += file.Size
		uploadedCount++
	}

	// Report results
	if len(failedFiles) > 0 {
		fmt.Fprintf(s.stdout, "\nUpload completed with %d failures:\n", len(failedFiles))
		for _, f := range failedFiles {
			fmt.Fprintf(s.stdout, "  - %s\n", f)
		}
	}
	fmt.Fprintf(s.stdout, "Upload complete: %d/%d files, %s/%s uploaded\n",
		uploadedCount, len(files), formatBytes(uploadedSize), formatBytes(totalSize))

	if len(failedFiles) > 0 {
		return fmt.Errorf("%d files failed to upload", len(failedFiles))
	}
	return nil
}

// localFileInfo holds information about a local file.
type localFileInfo struct {
	RelPath string
	Size    int64
}

// getLocalFileList recursively lists all files in a local directory.
func (s *Shell) getLocalFileList(localPath string) ([]localFileInfo, int64, error) {
	var files []localFileInfo
	var totalSize int64

	err := s.walkLocalDir(localPath, "", &files, &totalSize)
	if err != nil {
		return nil, 0, err
	}

	return files, totalSize, nil
}

// walkLocalDir recursively walks a local directory.
func (s *Shell) walkLocalDir(basePath, relPath string, files *[]localFileInfo, totalSize *int64) error {
	currentPath := basePath
	if relPath != "" {
		currentPath = filepath.Join(basePath, relPath)
	}

	entries, err := os.ReadDir(currentPath)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", currentPath, err)
	}

	for _, entry := range entries {
		entryRelPath := entry.Name()
		if relPath != "" {
			entryRelPath = filepath.Join(relPath, entry.Name())
		}

		if entry.IsDir() {
			// Recurse into subdirectory
			if err := s.walkLocalDir(basePath, entryRelPath, files, totalSize); err != nil {
				return err
			}
		} else {
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("get file info %s: %w", entryRelPath, err)
			}
			*files = append(*files, localFileInfo{
				RelPath: entryRelPath,
				Size:    info.Size(),
			})
			*totalSize += info.Size()
		}
	}

	return nil
}

// uploadSingleFileWithPrefix uploads a single file with a progress prefix.
func (s *Shell) uploadSingleFileWithPrefix(ctx context.Context, localPath, remotePath, prefix string) error {
	// Check if remote path is a directory, if so append the filename
	if stat, err := s.client.Stat(remotePath); err == nil && stat.Mode().IsDir() {
		remotePath = joinPath(remotePath, filepath.Base(localPath))
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

	// Create progress bar with prefix
	bar := progressbar.NewOptions64(
		fi.Size(),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription(fmt.Sprintf("%s %s", prefix, filepath.Base(localPath))),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetItsString("bytes"),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
	defer bar.Close()

	// Wrap reader with progress tracking
	progressReader := &progressReader{
		reader: srcFile,
		bar:    bar,
		size:   fi.Size(),
	}

	// Use io.CopyBuffer with large buffer
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
	colorGray      = "\033[90m"
	colorReset     = "\033[0m"
)

// cmdHelp shows help information.
func (s *Shell) cmdHelp() error {
	commands := []struct {
		cmd  string
		args string
		desc string
	}{
		{"cd", "<path>", "Change remote directory"},
		{"lcd", "<path>", "Change local directory"},
		{"pwd", "", "Print remote working directory"},
		{"lpwd", "", "Print local working directory"},
		{"ls", "[path]", "List remote files"},
		{"lls", "[path]", "List local files"},
		{"get", "<remote> [local]", "Download file or directory"},
		{"put", "<local> [remote]", "Upload file or directory"},
		{"mkdir", "<path>", "Create remote directory"},
		{"lmkdir", "<path>", "Create local directory"},
		{"exit", "", "Exit SFTP shell"},
		{"quit", "", "Exit SFTP shell (alias)"},
		{"bye", "", "Exit SFTP shell (alias)"},
	}

	// 上边框
	s.printTableLine("┌", "┬", "┐")

	// 表头
	s.printTableRow("COMMAND", "ARGUMENTS", "DESCRIPTION", colorGray, colorGray, colorGray)

	// 分隔线
	s.printTableLine("├", "┼", "┤")

	// 数据行
	for _, c := range commands {
		s.printTableRow(c.cmd, c.args, c.desc, colorGreen, colorReset, colorReset)
	}

	// 下边框
	s.printTableLine("└", "┴", "┘")

	return nil
}

// printTableLine prints a horizontal table line
func (s *Shell) printTableLine(left, mid, right string) {
	fmt.Fprintf(s.stdout, "  %s%s%s%s%s%s\n",
		left,
		strings.Repeat("─", cmdWidth+2),
		mid,
		strings.Repeat("─", argsWidth+2),
		mid,
		strings.Repeat("─", descWidth+2)+right)
}

// printTableRow prints a table row
func (s *Shell) printTableRow(col1, col2, col3, c1Color, c2Color, c3Color string) {
	fmt.Fprintf(s.stdout, "  │ %s%-*s%s │ %s%-*s%s │ %s%-*s%s │\n",
		c1Color, cmdWidth, col1, colorReset,
		c2Color, argsWidth, col2, colorReset,
		c3Color, descWidth, col3, colorReset)
}
