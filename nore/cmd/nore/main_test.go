package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/shared/types/apitoken"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
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

func TestResolveBuiltinPolicyWithoutAnArgumentUsesTheCeilings(t *testing.T) {
	policy, err := resolveBuiltinPolicy("")
	if err != nil {
		t.Fatalf("resolveBuiltinPolicy: %v", err)
	}

	ceilings := builtins.DefaultPolicy()
	if policy.MaxCommandOutputBytes != ceilings.MaxCommandOutputBytes {
		t.Errorf("maxCommandOutputBytes = %d, want %d", policy.MaxCommandOutputBytes, ceilings.MaxCommandOutputBytes)
	}
	if policy.DefaultTimeout != ceilings.DefaultTimeout {
		t.Errorf("defaultTimeout = %q, want %q", policy.DefaultTimeout, ceilings.DefaultTimeout)
	}
}

func TestResolveBuiltinPolicyAppliesAConfiguredPolicy(t *testing.T) {
	policy, err := resolveBuiltinPolicy(`{"allowedHTTPHosts":["api.example.com"],"maxHTTPResponseBytes":1024}`)
	if err != nil {
		t.Fatalf("resolveBuiltinPolicy: %v", err)
	}

	if len(policy.AllowedHTTPHosts) != 1 || policy.AllowedHTTPHosts[0] != "api.example.com" {
		t.Errorf("allowedHTTPHosts = %v, want [api.example.com]", policy.AllowedHTTPHosts)
	}
	if policy.MaxHTTPResponseBytes != 1024 {
		t.Errorf("maxHTTPResponseBytes = %d, want 1024", policy.MaxHTTPResponseBytes)
	}
}

func TestResolveBuiltinPolicyRefusesAnUnusablePolicy(t *testing.T) {
	if _, err := resolveBuiltinPolicy("{not json"); err == nil {
		t.Fatal("resolveBuiltinPolicy accepted a malformed policy")
	}
	if _, err := resolveBuiltinPolicy(`{"maxHTTPResponseBytes":1073741824}`); err == nil {
		t.Fatal("resolveBuiltinPolicy accepted a limit above the compiled-in ceiling")
	}
}

type nonTransactionalStore struct{}

func (nonTransactionalStore) Get(context.Context, string) ([]byte, error) {
	return nil, storage.ErrNotFound
}

func (nonTransactionalStore) Put(context.Context, string, []byte) error { return nil }

func (nonTransactionalStore) Delete(context.Context, string) error { return nil }

func (nonTransactionalStore) CompareAndSwap(context.Context, string, []byte, []byte) error {
	return storage.ErrNotFound
}

func (nonTransactionalStore) List(context.Context, string) ([]string, error) { return nil, nil }

func (nonTransactionalStore) Ping(context.Context) error { return nil }

func (nonTransactionalStore) WithTx(context.Context, func(storage.Tx) error) error {
	return errors.New("transactions are not supported")
}

func (nonTransactionalStore) Close() error { return nil }

func TestVerifyDurableBackendRejectsNonTransactionalStore(t *testing.T) {
	err := verifyDurableBackend(context.Background(), nonTransactionalStore{}, "nontx", instance.DurabilityDurable)
	if err == nil {
		t.Fatal("durable mode accepted a non-transactional backend")
	}
	for _, want := range []string{"durable mode", "transactional", "nontx"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestVerifyDurableBackendSkipsNonDurableModes(t *testing.T) {
	for _, mode := range []string{instance.DurabilityNone, instance.DurabilityHistory, ""} {
		if err := verifyDurableBackend(context.Background(), nonTransactionalStore{}, "nontx", mode); err != nil {
			t.Errorf("mode %q unexpectedly required transactions: %v", mode, err)
		}
	}
}

func TestEnvUnlessSetKeepsExplicitFlag(t *testing.T) {
	t.Setenv("NEURON_EXECUTION_DURABILITY", instance.DurabilityNone)

	if got := envUnlessSet(true, instance.DurabilityDurable, "NEURON_EXECUTION_DURABILITY"); got != instance.DurabilityDurable {
		t.Errorf("explicit flag value = %q, want %q", got, instance.DurabilityDurable)
	}
	if got := envUnlessSet(false, "", "NEURON_EXECUTION_DURABILITY"); got != instance.DurabilityNone {
		t.Errorf("environment value = %q, want %q", got, instance.DurabilityNone)
	}
}
