package sftp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/sftp"
)

// PathState manages dual CWD systems for SFTP.
//
// CRITICAL: SFTP protocol has NO current working directory.
// We must simulate TWO independent CWD systems:
// - LocalCWD  (for lcd / lls / lget / lput)
// - RemoteCWD (for cd / ls / get / put)
//
// After every successful cd, MUST call sftp.RealPath to prevent path drift.
type PathState struct {
	LocalCWD   string
	RemoteCWD  string
	HomeLocal  string
	HomeRemote string
	client     *sftp.Client
}

// NewPathState creates initial path state.
func NewPathState(client *sftp.Client) (*PathState, error) {
	// Get local home directory
	homeLocal, err := homedir.Dir()
	if err != nil {
		return nil, fmt.Errorf("get local home: %w", err)
	}

	// Get local working directory
	localCWD, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get local cwd: %w", err)
	}

	// Get remote home directory
	homeRemote, err := client.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get remote home: %w", err)
	}

	return &PathState{
		LocalCWD:   localCWD,
		RemoteCWD:  homeRemote,
		HomeLocal:  homeLocal,
		HomeRemote: homeRemote,
		client:     client,
	}, nil
}

// ResolveLocal resolves a local path relative to LocalCWD.
// Supports: ~ expansion, absolute paths, relative paths, ..
func (ps *PathState) ResolveLocal(path string) (string, error) {
	// Handle empty path
	if path == "" || path == "." {
		return ps.LocalCWD, nil
	}

	// Handle absolute path
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	// Handle ~ expansion
	if strings.HasPrefix(path, "~") {
		if len(path) > 1 && path[1] != '/' {
			return "", fmt.Errorf("~user not supported")
		}
		rest := strings.TrimPrefix(path, "~")
		if rest == "" {
			return ps.HomeLocal, nil
		}
		return filepath.Join(ps.HomeLocal, rest), nil
	}

	// Relative path - join with LocalCWD
	result := filepath.Join(ps.LocalCWD, path)
	return filepath.Clean(result), nil
}

// ResolveRemote resolves a remote path relative to RemoteCWD.
// Does NOT use filepath.Join - uses / as separator.
// Supports: ~ expansion, absolute paths, relative paths, ..
func (ps *PathState) ResolveRemote(path string) (string, error) {
	// Handle empty path
	if path == "" || path == "." {
		return ps.RemoteCWD, nil
	}

	// Handle absolute path
	if strings.HasPrefix(path, "/") {
		return cleanPath(path), nil
	}

	// Handle ~ expansion
	if strings.HasPrefix(path, "~") {
		if len(path) > 1 && path[1] != '/' {
			return "", fmt.Errorf("~user not supported")
		}
		rest := strings.TrimPrefix(path, "~")
		if rest == "" {
			return ps.HomeRemote, nil
		}
		return joinPath(ps.HomeRemote, rest), nil
	}

	// Relative path - join with RemoteCWD
	return joinPath(ps.RemoteCWD, path), nil
}

// UpdateRemoteCWD updates RemoteCWD after successful cd.
//
// CRITICAL: After every successful cd, MUST call sftp.RealPath
// to prevent path drift from symlinks and .. handling.
func (ps *PathState) UpdateRemoteCWD(path string) error {
	// After successful cd, ALWAYS resolve with RealPath
	// This prevents drift from symlinks and canonicalizes ..
	real, err := ps.client.RealPath(path)
	if err != nil {
		return fmt.Errorf("realpath %s: %w", path, err)
	}

	ps.RemoteCWD = real
	return nil
}

// UpdateLocalCWD updates LocalCWD after successful lcd.
func (ps *PathState) UpdateLocalCWD(path string) error {
	// For local paths, we can just use filepath.Clean
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("absolute path: %w", err)
	}

	ps.LocalCWD = abs
	return nil
}

// cleanPath cleans a remote path using / as separator.
func cleanPath(path string) string {
	// Split by /, clean each part, and rejoin
	parts := strings.Split(path, "/")
	var cleaned []string

	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(cleaned) > 0 {
				cleaned = cleaned[:len(cleaned)-1]
			}
		} else {
			cleaned = append(cleaned, part)
		}
	}

	result := "/" + strings.Join(cleaned, "/")
	if result == "/" {
		return result
	}
	return strings.TrimSuffix(result, "/")
}

// joinPath joins two remote path parts using / as separator.
func joinPath(base, rel string) string {
	if strings.HasSuffix(base, "/") {
		return base + rel
	}
	return base + "/" + rel
}
