package ssh

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ai-help-me/sshm/pkg/config"
	"golang.org/x/crypto/ssh"
)

// HostConfig contains SSH connection configuration.
type HostConfig struct {
	Host     string
	User     string
	Port     int
	Password string
	KeyPath  string
}

// NewHostConfig creates a HostConfig from a config.Host.
func NewHostConfig(host *config.Host) *HostConfig {
	return &HostConfig{
		Host:     host.Host,
		User:     host.User,
		Port:     host.Port,
		Password: host.Password,
		KeyPath:  host.KeyPath,
	}
}

// Client wraps an SSH client connection.
//
// This does NOT handle terminal lifecycle - that's terminal.Manager's job.
// Terminal Manager handles entering/exiting raw mode for interactive shells.
type Client struct {
	client   *ssh.Client
	config   *HostConfig
	jumpHost *config.Host
	mu       sync.Mutex
}

// NewClient creates a new SSH client for the given host.
func NewClient(host *config.Host) (*Client, error) {
	cfg := NewHostConfig(host)

	// Check if jump host is needed
	if host.Jump != nil && len(host.Jump) > 0 {
		// Will be handled by JumpChain
		return nil, fmt.Errorf("use JumpChain for hosts with jump configuration")
	}

	return &Client{
		config: cfg,
	}, nil
}

// Dial establishes an SSH connection.
func (c *Client) Dial() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	authMethods, err := AuthMethods(c.config)
	if err != nil {
		return fmt.Errorf("get auth methods: %w", err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            c.config.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)

	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, sshConfig)
	if err != nil {
		conn.Close()
		return fmt.Errorf("ssh connection to %s: %w", addr, err)
	}

	c.client = ssh.NewClient(sshConn, chans, reqs)
	return nil
}

// Session creates a new SSH session.
//
// Caller is responsible for terminal lifecycle:
// - This function does NOT modify terminal mode
// - Use terminal.Manager.EnterRaw() before interactive shell
// - Use terminal.Manager.Restore() after shell ends
func (c *Client) Session() (*ssh.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	return c.client.NewSession()
}

// SFTPClient creates a new SFTP client.
func (c *Client) SFTPClient() (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	return c.client, nil
}

// GetSSHClient returns the underlying SSH client for SFTP operations.
func (c *Client) GetSSHClient() *ssh.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

// Close closes the SSH connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// IsConnected returns true if the client is connected.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client != nil
}

// GetUser returns the username for this client.
func (c *Client) GetUser() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config != nil {
		return c.config.User
	}
	return ""
}

// GetHost returns the hostname for this client.
func (c *Client) GetHost() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config != nil {
		return c.config.Host
	}
	return ""
}
