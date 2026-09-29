package connection

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/neuron-runtime/neuron/shared/types/apitoken"
)

// recordingServer captures the Authorization header of every request so the
// credential can be asserted on the wire rather than on the client's intent.
func recordingServer(t *testing.T, seen chan<- string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get(apitoken.AuthorizationHeader):
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPTransportSendsAuthorizationHeader(t *testing.T) {
	seen := make(chan string, 1)
	srv := recordingServer(t, seen)

	transport := NewHTTPTransport(srv.Client(), srv.URL).WithToken("secret")

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"Do", func() error {
			return transport.Do(context.Background(), http.MethodGet, "/v1/instances", nil, nil)
		}},
		{"Stream", func() error {
			return transport.Stream(context.Background(), http.MethodGet, "/v1/stream", nil, func([]byte) error { return nil })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			select {
			case got := <-seen:
				if got != "Bearer secret" {
					t.Fatalf("Authorization = %q, want %q", got, "Bearer secret")
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s: no request reached the server", tc.name)
			}
		})
	}
}

func TestHTTPTransportWithoutTokenSendsNoAuthorization(t *testing.T) {
	seen := make(chan string, 1)
	srv := recordingServer(t, seen)

	if err := NewHTTPTransport(srv.Client(), srv.URL).
		Do(context.Background(), http.MethodGet, "/health", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}

	select {
	case got := <-seen:
		if got != "" {
			t.Fatalf("Authorization = %q, want no credential", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no request reached the server")
	}
}

func TestOpenWebSocketAuthenticatesUpgrade(t *testing.T) {
	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get(apitoken.AuthorizationHeader):
		default:
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"neuron.v1"}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := NewHTTPTransport(srv.Client(), srv.URL).
		WithToken("secret").
		OpenWebSocket(ctx, "/v1/ws")
	if err != nil {
		t.Fatalf("OpenWebSocket: %v", err)
	}
	defer stream.Close()

	select {
	case got := <-seen:
		if got != "Bearer secret" {
			t.Fatalf("upgrade Authorization = %q, want %q", got, "Bearer secret")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no upgrade reached the server")
	}
}

func TestLocalAPITokenReadsTokenFileBesideSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "nore.sock")
	if err := apitoken.Write(apitoken.TokenFilePath(socket), "from-file"); err != nil {
		t.Fatalf("write token: %v", err)
	}

	got, ok := LocalAPIToken(socket)
	if !ok || got != "from-file" {
		t.Fatalf("LocalAPIToken = %q, %v; want %q, true", got, ok, "from-file")
	}
}

func TestLocalAPITokenPrefersExplicitEnvironment(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "nore.sock")
	if err := apitoken.Write(apitoken.TokenFilePath(socket), "from-file"); err != nil {
		t.Fatalf("write token: %v", err)
	}
	t.Setenv(apitoken.EnvToken, "from-env")

	got, ok := LocalAPIToken(socket)
	if !ok || got != "from-env" {
		t.Fatalf("LocalAPIToken = %q, %v; want the environment token", got, ok)
	}
}

func TestLocalAPITokenMissingFileIsNotAnError(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "nore.sock")

	if got, ok := LocalAPIToken(socket); ok {
		t.Fatalf("LocalAPIToken = %q, true for a missing token file", got)
	}
}

func TestNewRemoteUsesEnvironmentToken(t *testing.T) {
	seen := make(chan string, 1)
	srv := recordingServer(t, seen)
	t.Setenv(apitoken.EnvToken, "remote-token")

	conn := NewRemote(srv.URL)
	defer conn.Close()

	if err := conn.Do(context.Background(), http.MethodGet, "/v1/instances", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}

	select {
	case got := <-seen:
		if got != "Bearer remote-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer remote-token")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no request reached the server")
	}
}

// TestTokenIsNotSentInURL documents that the credential never appears in a
// request URL: URLs end up in access logs, proxy logs, and error messages.
func TestTokenIsNotSentInURL(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	transport := NewHTTPTransport(srv.Client(), srv.URL).WithToken("secret")
	if err := transport.Do(ctx, http.MethodGet, "/v1/instances", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if strings.Contains(path, "secret") || strings.Contains(path, "token") {
		t.Fatalf("request URL %q leaks the credential", path)
	}
}
