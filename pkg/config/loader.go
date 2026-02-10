package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
)

// Load reads and parses the configuration from the specified path.
// If path is empty, loads and merges ~/.sshm.yaml and ~/.sshw.yaml in order.
// Expands ~ in the path before reading.
func Load(path string) (*Config, error) {
	if path == "" {
		return loadDefaultConfigs()
	}

	// Expand ~ in path
	expandedPath, err := expandPath(path)
	if err != nil {
		return nil, fmt.Errorf("expand config path: %w", err)
	}

	return loadSingleConfig(expandedPath)
}

// loadDefaultConfigs loads and merges ~/.sshm.yaml and ~/.sshw.yaml
func loadDefaultConfigs() (*Config, error) {
	paths, err := DefaultConfigPaths()
	if err != nil {
		return nil, err
	}

	var allHosts []*Host
	var loadedCount int

	for _, path := range paths {
		expandedPath, err := expandPath(path)
		if err != nil {
			continue
		}

		// Check if file exists
		if _, err := os.Stat(expandedPath); os.IsNotExist(err) {
			continue
		}

		cfg, err := loadSingleConfig(expandedPath)
		if err != nil {
			// Log warning but continue with other files
			fmt.Fprintf(os.Stderr, "Warning: failed to load %s: %v\n", expandedPath, err)
			continue
		}

		allHosts = append(allHosts, cfg.Hosts...)
		loadedCount++
	}

	if loadedCount == 0 {
		return nil, fmt.Errorf("no config files found (tried: %v)", paths)
	}

	// Create merged config
	cfg := &Config{
		Hosts: allHosts,
	}

	// Validate all hosts
	for i, host := range cfg.Hosts {
		if err := host.Validate(); err != nil {
			return nil, fmt.Errorf("validate host #%d (%s): %w", i, host.Name, err)
		}
	}

	return cfg, nil
}

// loadSingleConfig loads a single config file
func loadSingleConfig(expandedPath string) (*Config, error) {
	// Read file
	data, err := os.ReadFile(expandedPath)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", expandedPath, err)
	}

	// Try parsing as a list of hosts directly (the expected format)
	var hosts []*Host
	if err := yaml.Unmarshal(data, &hosts); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	// Create config from the hosts
	cfg := &Config{
		Hosts: hosts,
	}

	// Validate all hosts
	for i, host := range cfg.Hosts {
		if err := host.Validate(); err != nil {
			return nil, fmt.Errorf("validate host #%d (%s): %w", i, host.Name, err)
		}
	}

	return cfg, nil
}

// Save writes the configuration to the specified path.
func Save(cfg *Config, path string) error {
	// Expand ~ in path
	expandedPath, err := expandPath(path)
	if err != nil {
		return fmt.Errorf("expand config path: %w", err)
	}

	// Marshal to YAML
	data, err := yaml.Marshal(cfg.Hosts)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}

	// Write file
	if err := os.WriteFile(expandedPath, data, 0600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}
