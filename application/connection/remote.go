package connection

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/apitoken"
)

// NewRemote returns a client for a N.O.R.E. API reachable over HTTP.
//
// A remote endpoint is reachable by anything that can route to it, so the
// daemon refuses to serve TCP without a token and this client refuses to call
// one without presenting it. The credential comes from NEURON_API_TOKEN, since
// there is no local token file to read on a remote host.
func NewRemote(endpoint string) Connection {
	endpoint = strings.TrimRight(endpoint, "/")
	transport := NewHTTPTransport(&http.Client{Timeout: 60 * time.Second}, endpoint)
	if token := strings.TrimSpace(os.Getenv(apitoken.EnvToken)); token != "" {
		transport.WithToken(token)
	}
	return New(transport)
}
