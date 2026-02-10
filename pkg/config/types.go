package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/go-homedir"
)

// Host represents a single SSH host configuration.
type Host struct {
	Name           string   `yaml:"name"`
	Host           string   `yaml:"host"`
	User           string   `yaml:"user"`
	Port           int      `yaml:"port"`
	Password       string   `yaml:"password,omitempty"`
	KeyPath        string   `yaml:"keypath,omitempty"`
	Jump           []*Host  `yaml:"jump,omitempty"`
	Children       []*Host  `yaml:"children,omitempty"`
	CallbackShells []string `yaml:"callback-shells,omitempty"`
}

// Validate checks that the host has all required fields.
// Group entries (with children) only require a name.
func (h *Host) Validate() error {
	var errs []string

	if h.Name == "" {
		errs = append(errs, "name is required")
	}

	// Group entries don't need host/user - they're just containers
	if len(h.Children) == 0 {
		// This is a leaf node, requires host and user
		if h.Host == "" {
			errs = append(errs, "host is required")
		}

		if h.User == "" {
			errs = append(errs, "user is required")
		}
	}

	if h.Port == 0 {
		h.Port = 22 // Default SSH port
	}

	// Authentication is optional - can use SSH agent or keyboard-interactive

	// Expand ~ in keypath
	if h.KeyPath != "" {
		expanded, err := expandPath(h.KeyPath)
		if err != nil {
			return fmt.Errorf("keypath expansion: %w", err)
		}
		h.KeyPath = expanded
	}

	if len(errs) > 0 {
		return fmt.Errorf("host validation errors: %s", strings.Join(errs, ", "))
	}

	return nil
}

// Config is the root configuration structure.
type Config struct {
	Hosts []*Host `yaml:"hosts"`
}

// GetHostsAtPath returns the hosts at the given path.
// Empty path returns top-level hosts.
func (c *Config) GetHostsAtPath(path []string) []*Host {
	if len(path) == 0 {
		return c.Hosts
	}

	// Navigate to the host at path
	current := c.findHostByPath(c.Hosts, path)
	if current == nil {
		return nil
	}

	return current.Children
}

// findHostByPath finds a host by navigating through the path segments.
func (c *Config) findHostByPath(hosts []*Host, path []string) *Host {
	if len(path) == 0 {
		return nil
	}

	// Find first segment
	for _, host := range hosts {
		if host.Name == path[0] {
			if len(path) == 1 {
				return host
			}
			// Recurse with remaining path
			return c.findHostByPath(host.Children, path[1:])
		}
	}

	return nil
}

// FindHost locates a host by full name (supports nested paths like "k3s/192.168.1.16").
func (c *Config) FindHost(name string) *Host {
	// Split by path separator
	path := strings.Split(name, "/")

	// Start from root hosts
	currentLevel := c.Hosts

	// Navigate through path
	for i, segment := range path {
		found := false
		for _, host := range currentLevel {
			if host.Name == segment {
				// If this is the last segment, return the host
				if i == len(path)-1 {
					return host
				}
				// Otherwise, continue to children
				currentLevel = host.Children
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}

	return nil
}

// expandPath expands ~ to the home directory.
func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := homedir.Dir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}

	if strings.HasPrefix(path, "~") {
		return homedir.Dir()
	}

	return path, nil
}

// DefaultConfigPath returns the default configuration file path (~/.sshm.yaml).
func DefaultConfigPath() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(home, ".sshm.yaml"), nil
}

// DefaultConfigPaths returns the list of default configuration file paths.
// Loads in order: ~/.sshm.yaml, then ~/.sshw.yaml
func DefaultConfigPaths() ([]string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}
	return []string{
		filepath.Join(home, ".sshm.yaml"),
		filepath.Join(home, ".sshw.yaml"),
	}, nil
}

// Exists checks if the config file exists.
func (c *Config) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
