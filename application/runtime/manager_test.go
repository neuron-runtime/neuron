package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/connection"
	"github.com/Muhammad-Jay/neuron/application/daemon"
)

// stubConnection implements connection.Connection to simulate a healthy or
// unreachable runtime without a real socket.
type stubConnection struct {
	healthErr error
}

func (s *stubConnection) Health(ctx context.Context) error { return s.healthErr }
func (s *stubConnection) Do(ctx context.Context, method, path string, body any, out any) error {
	return nil
}
func (s *stubConnection) Stream(ctx context.Context, method, path string, body any, emit func([]byte) error) error {
	return nil
}
func (s *stubConnection) OpenWebSocket(ctx context.Context, requestPath string) (*connection.WebSocketStream, error) {
	return nil, nil
}
func (s *stubConnection) Close() error { return nil }

func TestEnsureHealthyDoesNotResolveBinary(t *testing.T) {
	var resolverCalls int

	m := NewManager(
		daemon.Config{},
		&stubConnection{healthErr: nil},
		func() (string, error) { resolverCalls++; return "", errors.New("binary must not be resolved") },
	)

	if err := m.Ensure(context.Background(), false); err != nil {
		t.Fatalf("Ensure on a healthy runtime should succeed, got: %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("binary resolver was called %d times; a healthy daemon needs no binary", resolverCalls)
	}
}

func TestEnsureHealthyRemoteDoesNotResolveBinary(t *testing.T) {
	var resolverCalls int

	m := NewManager(
		daemon.Config{},
		&stubConnection{healthErr: nil},
		func() (string, error) { resolverCalls++; return "", errors.New("binary must not be resolved") },
	)

	if err := m.Ensure(context.Background(), true); err != nil {
		t.Fatalf("Ensure on a healthy remote runtime should succeed, got: %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("binary resolver was called %d times; a healthy remote needs no binary", resolverCalls)
	}
}

func TestEnsureDownLocalResolvesBinary(t *testing.T) {
	m := NewManager(
		daemon.Config{},
		&stubConnection{healthErr: errors.New("connection refused")},
		func() (string, error) { return "", nil },
	)

	// A down local daemon must consult the resolver before attempting a start.
	// Here the resolver returns an empty path (as a missing binary would come
	// from NoreBinaryPath with a clearer error), so the failure surfaces from
	// process start rather than binary discovery.
	if err := m.Ensure(context.Background(), false); err == nil {
		t.Fatal("Ensure on a down local runtime should fail when no daemon can start")
	}
}

func TestEnsureRemoteDownDoesNotResolveBinary(t *testing.T) {
	downErr := errors.New("connection refused")
	var resolverCalls int

	m := NewManager(
		daemon.Config{},
		&stubConnection{healthErr: downErr},
		func() (string, error) { resolverCalls++; return "", nil },
	)

	if err := m.Ensure(context.Background(), true); !errors.Is(err, downErr) {
		t.Fatalf("Ensure on a down remote should surface the health error, got: %v", err)
	}
	if resolverCalls != 0 {
		t.Fatalf("binary resolver was called %d times; a remote never starts a local daemon", resolverCalls)
	}
}

var _ connection.Connection = (*stubConnection)(nil)
