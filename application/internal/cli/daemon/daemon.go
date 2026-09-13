package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Muhammad-Jay/neuron/application/config"
	noredaemon "github.com/Muhammad-Jay/neuron/application/daemon"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/ui"
	"github.com/spf13/cobra"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Daemon,
		Short: "Manage the local N.O.R.E. daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newStopCmd())

	return cmd
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background N.O.R.E. daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()

			cfg, ok := config.FromContext(cmd.Context())
			if !ok {
				return fmt.Errorf("configuration not loaded")
			}

			manager := noredaemon.NewManager(noredaemon.ConfigFromEffective(cfg), nil)

			// Daemon lifecycle is progress, not a machine-readable record:
			// render it on stderr through the step runner.
			rep := ui.New(cmd.ErrOrStderr())
			defer rep.Stop()

			rep.Info("Stopping N.O.R.E. daemon...")

			if err := manager.Stop(ctx); err != nil {
				if errors.Is(err, noredaemon.ErrNotRunning) {
					rep.Info("Daemon is not currently running.")
					return nil
				}
				rep.Fail("Failed to stop the daemon")
				return fmt.Errorf("failed to stop daemon: %w", err)
			}

			rep.OK("Daemon stopped successfully.")
			return nil
		},
	}
}
