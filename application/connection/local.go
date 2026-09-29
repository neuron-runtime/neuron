package connection

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/apitoken"
)

func NewLocal(socketPath string) Connection {
	socketPath = filepath.Clean(socketPath)

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	client := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
	}

	// The host is intentionally fake. The Unix socket determines the destination.
	transport := NewHTTPTransport(client, "http://nore.local")
	if token, ok := LocalAPIToken(socketPath); ok {
		transport.WithToken(token)
	}
	return New(transport)
}

// LocalAPIToken resolves the credential for a local daemon.
//
// The daemon publishes the token it requires beside its socket, so a client
// given a socket path can authenticate without configuration. An explicit
// NEURON_API_TOKEN wins, which is how a client authenticates against a remote
// endpoint that has no local token file.
func LocalAPIToken(socketPath string) (string, bool) {
	if token := strings.TrimSpace(os.Getenv(apitoken.EnvToken)); token != "" {
		return token, true
	}
	path := os.Getenv(apitoken.EnvTokenFile)
	if path == "" {
		path = apitoken.TokenFilePath(socketPath)
	}
	return apitoken.Read(path)
}

func LocalSocketExists(socketPath string) bool {
	_, err := os.Stat(socketPath)
	return err == nil
}
