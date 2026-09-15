package executor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// newSocketListener creates a Unix domain socket listener. The parent socket
// directory must already exist. A pre-existing socket file is removed so a
// stale socket from a crashed process does not block startup.
func newSocketListener(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}

	// Remove a stale socket file if present.
	if _, err := os.Lstat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	return listener, nil
}

// touchFile creates a file if it does not exist, signaling readiness.
func touchFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
