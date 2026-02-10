package ssh

import (
	"fmt"
	"net"
	"sync"

	"github.com/ai-help-me/sshm/pkg/config"
	"golang.org/x/crypto/ssh"
)

// JumpChain implements transparent multi-hop SSH connections (ProxyJump style).
//
// Example: localhost -> jump1 -> jump2 -> target
type JumpChain struct {
	hosts   []*config.Host
	clients []*ssh.Client
	mu      sync.Mutex
}

// NewJumpChain creates a new jump chain from a host's jump configuration.
func NewJumpChain(host *config.Host) *JumpChain {
	return &JumpChain{
		hosts: host.Jump,
	}
}

// NewJumpChainWithTarget creates a jump chain that includes the final target.
func NewJumpChainWithTarget(host *config.Host) *JumpChain {
	// Build the full chain: jump hosts + target as final destination
	chain := make([]*config.Host, 0, len(host.Jump)+1)
	chain = append(chain, host.Jump...)
	chain = append(chain, host)

	return &JumpChain{
		hosts: chain,
	}
}

// Connect establishes connections through all jump hosts.
//
// Returns the final SSH client connected to the target host.
// The caller should call Close() when done to clean up all connections.
func (jc *JumpChain) Connect() (*ssh.Client, error) {
	jc.mu.Lock()
	defer jc.mu.Unlock()

	var prevClient *ssh.Client

	for i, host := range jc.hosts {
		client, err := jc.connectHop(host, prevClient)
		if err != nil {
			// Clean up previous connections on failure
			jc.closeAll()
			return nil, fmt.Errorf("hop %d (%s): %w", i+1, host.Name, err)
		}

		jc.clients = append(jc.clients, client)
		prevClient = client
	}

	// Return the final client (connected to target)
	return jc.clients[len(jc.clients)-1], nil
}

// connectHop connects to a single hop in the chain.
func (jc *JumpChain) connectHop(host *config.Host, prevClient *ssh.Client) (*ssh.Client, error) {
	var conn net.Conn
	var err error

	addr := fmt.Sprintf("%s:%d", host.Host, host.Port)

	if prevClient == nil {
		// First hop - direct connection from local machine
		conn, err = net.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("direct dial %s: %w", addr, err)
		}
	} else {
		// Subsequent hop - forward through previous SSH client
		conn, err = prevClient.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("dial through proxy to %s: %w", addr, err)
		}
	}

	// Create SSH config with authentication
	authMethods, err := AuthMethodsFromConfig(host.KeyPath, host.Password)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("auth methods for %s: %w", host.Name, err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            host.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * 1000000000, // 30 seconds in nanoseconds
	}

	// Establish SSH connection over the TCP connection
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, sshConfig)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh conn to %s: %w", host.Name, err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

// Close closes all SSH connections in reverse order.
func (jc *JumpChain) Close() error {
	jc.mu.Lock()
	defer jc.mu.Unlock()
	return jc.closeAll()
}

// closeAll closes all connections without locking (internal use).
func (jc *JumpChain) closeAll() error {
	var lastErr error

	// Close in reverse order (target first, then jump hosts)
	for i := len(jc.clients) - 1; i >= 0; i-- {
		if err := jc.clients[i].Close(); err != nil {
			lastErr = err
		}
	}

	jc.clients = nil
	return lastErr
}

// GetSSHClient returns the underlying SSH client for SFTP operations.
func (jc *JumpChain) GetSSHClient() *ssh.Client {
	jc.mu.Lock()
	defer jc.mu.Unlock()

	if len(jc.clients) == 0 {
		return nil
	}
	return jc.clients[len(jc.clients)-1]
}

// Session creates a new SSH session on the target host.
func (jc *JumpChain) Session() (*ssh.Session, error) {
	client := jc.GetSSHClient()
	if client == nil {
		return nil, fmt.Errorf("not connected")
	}
	return client.NewSession()
}

// IsConnected returns true if the jump chain is connected.
func (jc *JumpChain) IsConnected() bool {
	jc.mu.Lock()
	defer jc.mu.Unlock()
	return len(jc.clients) > 0
}
