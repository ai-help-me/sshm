package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
)

// AuthMethods returns authentication methods for a host configuration.
// Priority: key auth > password auth.
// Returns empty list for SSH agent or keyboard-interactive auth.
func AuthMethods(host *HostConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Try key authentication first
	if host.KeyPath != "" {
		keyAuth, err := keyAuthMethod(host.KeyPath)
		if err == nil {
			methods = append(methods, keyAuth)
		} else {
			// If key fails, we'll still try password
			// Log the error but don't fail
			fmt.Fprintf(os.Stderr, "Warning: key auth failed: %v\n", err)
		}
	}

	// Add password authentication
	if host.Password != "" {
		methods = append(methods, ssh.Password(host.Password))
	}

	// Return empty list for SSH agent or keyboard-interactive auth
	return methods, nil
}

// AuthMethodsFromConfig creates authentication methods from individual config values.
// Returns empty list for SSH agent or keyboard-interactive auth.
func AuthMethodsFromConfig(keyPath, password string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Try key authentication first
	if keyPath != "" {
		keyAuth, err := keyAuthMethod(keyPath)
		if err == nil {
			methods = append(methods, keyAuth)
		}
	}

	// Add password authentication
	if password != "" {
		methods = append(methods, ssh.Password(password))
	}

	// Return empty list for SSH agent or keyboard-interactive auth
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
