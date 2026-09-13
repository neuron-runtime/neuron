package runtime

import (
	"context"

	"github.com/Muhammad-Jay/neuron/application/connection"
	"github.com/Muhammad-Jay/neuron/application/daemon"
)

// connectionHealthAdapter adapts the connection interface to the daemon health
// checker.
type connectionHealthAdapter struct {
	conn connection.Connection
}

func (c connectionHealthAdapter) Healthy(ctx context.Context) error {
	return c.conn.Health(ctx)
}

// BinaryResolver returns the path to the nore daemon binary on demand. It is
// consulted only when a local daemon must actually be started, so commands that
// reuse an already-running daemon never require the binary to be discoverable.
type BinaryResolver func() (string, error)

type Manager struct {
	Conn   connection.Connection
	cfg    daemon.Config
	health daemon.HealthChecker
	binary BinaryResolver
}

func NewManager(cfg daemon.Config, conn connection.Connection, binary BinaryResolver) *Manager {
	return &Manager{
		Conn:   conn,
		cfg:    cfg,
		health: connectionHealthAdapter{conn: conn},
		binary: binary,
	}
}

// Ensure guarantees that the N.O.R.E connection is ready.
// If it's a local connection and N.O.R.E is down, it starts it.
func (m *Manager) Ensure(ctx context.Context, isRemote bool) error {
	// If it's already healthy (local or remote), we are good to go.
	if err := m.Conn.Health(ctx); err == nil {
		return nil
	}

	// If the user specified a remote URL, we should not try to start a local daemon.
	if isRemote {
		return m.Conn.Health(ctx) // Return the actual connection error
	}

	// It's local and down. Only now is the daemon binary required.
	binaryPath, err := m.binary()
	if err != nil {
		return err
	}
	m.cfg.BinaryPath = binaryPath

	// Let the daemon manager start it and wait for health.
	return daemon.NewManager(m.cfg, m.health).Start(ctx)
}