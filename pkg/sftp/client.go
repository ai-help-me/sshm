package sftp

import (
	"fmt"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// NewClient creates a new SFTP client from an SSH client.
//
// Performance optimizations enabled:
// - Concurrent reads/writes for better throughput
// - Larger packet size (256KB) to reduce round trips and improve throughput
// - Higher concurrent request limit (64) for pipelining
//
// These optimizations can improve transfer speeds from ~9MB/s to 100+MB/s
// in high-latency networks (100ms+).
// Reference: https://pkg.go.dev/github.com/pkg/sftp
func NewClient(sshClient *ssh.Client) (*sftp.Client, error) {
	// Reduce concurrent requests to avoid connection instability
	// Some SFTP servers may close connections with too many concurrent requests
	client, err := sftp.NewClient(sshClient,
		sftp.UseConcurrentWrites(true),
	)
	if err != nil {
		return nil, fmt.Errorf("create sftp client: %w", err)
	}
	return client, nil
}
