package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/apitoken"
)

func TestResolveAPITokenPrefersExplicitFlag(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "nore.sock")
	if err := apitoken.Write(tokenPathFor(socket), "from-file"); err != nil {
		t.Fatalf("write token: %v", err)
	}

	token, generated, err := resolveAPIToken("from-flag", socket)
	if err != nil {
		t.Fatalf("resolveAPIToken: %v", err)
	}
	if token != "from-flag" {
		t.Fatalf("token = %q, want the flag value", token)
	}
	if generated {
		t.Fatal("an explicit token must not be reported as generated")
	}
}

func TestResolveAPITokenReusesExistingTokenFile(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "nore.sock")
	if err := apitoken.Write(tokenPathFor(socket), "persisted"); err != nil {
		t.Fatalf("write token: %v", err)
	}

	token, generated, err := resolveAPIToken("", socket)
	if err != nil {
		t.Fatalf("resolveAPIToken: %v", err)
	}
	if token != "persisted" {
		t.Fatalf("token = %q, want the persisted value", token)
	}
	if generated {
		t.Fatal("a pre-existing token file must not be reported as generated")
	}
}

func TestResolveAPITokenGeneratesOwnerOnlyTokenFile(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "nore.sock")

	token, generated, err := resolveAPIToken("", socket)
	if err != nil {
		t.Fatalf("resolveAPIToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected a generated token")
	}
	if !generated {
		t.Fatal("a generated token must be reported as generated so shutdown can remove it")
	}

	path := tokenPathFor(socket)
	stored, ok := apitoken.Read(path)
	if !ok || stored != token {
		t.Fatalf("stored token = %q, %v; want %q, true", stored, ok, token)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file permissions = %v, want -rw-------", perm)
	}
}

func TestResolveAPITokenWithoutSocketYieldsNoToken(t *testing.T) {
	token, generated, err := resolveAPIToken("", "")
	if err != nil {
		t.Fatalf("resolveAPIToken: %v", err)
	}
	if token != "" || generated {
		t.Fatalf("resolveAPIToken = %q, %v; want no token for a TCP-only daemon", token, generated)
	}
}

func TestTokenPathForHonorsEnvironmentOverride(t *testing.T) {
	t.Setenv(apitoken.EnvTokenFile, "/custom/credential")

	if got := tokenPathFor("/run/neuron/nore.sock"); got != "/custom/credential" {
		t.Fatalf("tokenPathFor = %q, want the override", got)
	}
}

// With no explicit port the OS assigns one, and the bound listener reports the
// real port. Retaining this listener is what makes concurrent project runtimes
// safe: the OS resolves the race instead of the process probing and racing.
func TestListenProjectAutoAssignsPort(t *testing.T) {
	listener, err := listenProject("127.0.0.1", "")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	if port == "" || port == "0" {
		t.Fatalf("auto port = %q, want a concrete assigned port", port)
	}
}

// An explicit port binds exactly that port.
func TestListenProjectExplicitPort(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	_, port, err := net.SplitHostPort(probe.Addr().String())
	if err != nil {
		t.Fatalf("split probe addr: %v", err)
	}
	probe.Close()

	listener, err := listenProject("127.0.0.1", port)
	if err != nil {
		t.Fatalf("listen on %s: %v", port, err)
	}
	defer listener.Close()

	_, got, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	if got != port {
		t.Errorf("bound port = %q, want the explicit %q", got, port)
	}
}

// An occupied port must fail cleanly rather than silently binding elsewhere,
// so an operator who asked for a specific port learns it is taken.
func TestListenProjectOccupiedPortFails(t *testing.T) {
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer first.Close()

	_, port, err := net.SplitHostPort(first.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}

	if listener, err := listenProject("127.0.0.1", port); err == nil {
		listener.Close()
		t.Fatalf("binding occupied port %s succeeded, want an error", port)
	}
}
