package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"net"
)

// Default SSH key paths to try when no keypath is specified
var defaultKeyPaths = []string{
	"~/.ssh/id_ed25519",
	"~/.ssh/id_rsa",
	"~/.ssh/id_ecdsa",
	"~/.ssh/id_dsa",
}

// AuthMethods returns authentication methods for a host configuration.
// Priority: key auth > password auth > ssh agent.
func AuthMethods(host *HostConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Try key authentication first (explicit keypath)
	if host.KeyPath != "" {
		keyAuth, err := keyAuthMethod(host.KeyPath)
		if err == nil {
			methods = append(methods, keyAuth)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: key auth failed: %v\n", err)
		}
	} else {
		// No explicit keypath, try default SSH keys
		for _, keyPath := range defaultKeyPaths {
			expandedPath := expandPath(keyPath)
			keyAuth, err := keyAuthMethod(expandedPath)
			if err == nil {
				methods = append(methods, keyAuth)
				break // Use first valid key found
			}
		}
	}

	// Add password authentication
	if host.Password != "" {
		methods = append(methods, ssh.Password(host.Password))
	}

	// Try SSH agent as fallback
	if agentAuth := trySSHAgent(); agentAuth != nil {
		methods = append(methods, agentAuth)
	}

	return methods, nil
}

// AuthMethodsFromConfig creates authentication methods from individual config values.
// Also tries default keys and SSH agent if no explicit key provided.
func AuthMethodsFromConfig(keyPath, password string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Try key authentication first (explicit keypath)
	if keyPath != "" {
		keyAuth, err := keyAuthMethod(keyPath)
		if err == nil {
			methods = append(methods, keyAuth)
		}
	} else {
		// No explicit keypath, try default SSH keys
		for _, defaultPath := range defaultKeyPaths {
			expandedPath := expandPath(defaultPath)
			keyAuth, err := keyAuthMethod(expandedPath)
			if err == nil {
				methods = append(methods, keyAuth)
				break // Use first valid key found
			}
		}
	}

	// Add password authentication
	if password != "" {
		methods = append(methods, ssh.Password(password))
	}

	// Try SSH agent as fallback
	if agentAuth := trySSHAgent(); agentAuth != nil {
		methods = append(methods, agentAuth)
	}

	return methods, nil
}

// keyAuthMethod creates an SSH auth method from a private key file.
func keyAuthMethod(keyPath string) (ssh.AuthMethod, error) {
	// Read key file
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}

	// Try to parse as various key formats
	var signers []ssh.Signer

	// Try PKCS1/PKCS8 format
	signer, err := ssh.ParsePrivateKey(keyData)
	if err == nil {
		signers = append(signers, signer)
	}

	// Try encrypted private key
	if len(signers) == 0 {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(keyData, []byte{})
		if err == nil {
			signers = append(signers, signer)
		}
	}

	// Try PEM block format
	if len(signers) == 0 {
		block, _ := pem.Decode(keyData)
		if block != nil {
			signer, err = ssh.ParsePrivateKey(keyData)
			if err == nil {
				signers = append(signers, signer)
			}
		}
	}

	if len(signers) == 0 {
		return nil, fmt.Errorf("no valid key found in %s", keyPath)
	}

	return ssh.PublicKeys(signers[0]), nil
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// trySSHAgent attempts to connect to SSH agent and return auth method
func trySSHAgent() ssh.AuthMethod {
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		return nil
	}

	conn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err != nil {
		return nil
	}
	defer conn.Close()

	ag := agent.NewClient(conn)
	signers, err := ag.Signers()
	if err != nil || len(signers) == 0 {
		return nil
	}

	return ssh.PublicKeys(signers...)
}

// GenerateKey generates a new RSA key pair for testing.
func GenerateKey() ([]byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	return pem.EncodeToMemory(privateKeyPEM), nil
}
