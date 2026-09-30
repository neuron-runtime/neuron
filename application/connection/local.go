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

// Local is a connection to a local N.O.R.E. daemon over its Unix socket.
//
// The daemon publishes the API token it requires beside its socket, so a
// client resolves the credential from the socket path. A daemon that is not
// running yet has no token file, which is why the credential can be resolved
// again with LoadAPIToken once the daemon has started.
type Local struct {
	Connection

	socketPath string
	transport  *HTTPTransport
}

func NewLocal(socketPath string) *Local {
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
	local := &Local{
		Connection: New(transport),
		socketPath: socketPath,
		transport:  transport,
	}
	local.LoadAPIToken()
	return local
}

// LoadAPIToken resolves the credential published beside the socket and applies
// it to subsequent requests. Callers that may have connected before a local
// daemon was running must call this once the daemon is up; otherwise every
// request is rejected as unauthenticated.
func (l *Local) LoadAPIToken() {
	if token, ok := LocalAPIToken(l.socketPath); ok {
		l.transport.WithToken(token)
	}
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
