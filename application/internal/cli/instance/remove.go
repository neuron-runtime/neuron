package instance

import (
	"context"
	"fmt"

	"github.com/Muhammad-Jay/neuron/application/client"
	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/bootstrap"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/utils"
	"github.com/spf13/cobra"
)

func newRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     command.InstanceRemove,
		Aliases: []string{"rm"},
		Short:   "Remove an instance and its executions and events",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			target, err := utils.NormalizeInstanceTarget(args[0])
			if err != nil {
				return err
			}

			c, cleanup, err := setup(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := c.RemoveInstance(ctx, target); err != nil {
				return fmt.Errorf("remove instance %s: %w", target, err)
			}

			fmt.Printf("removed instance %s\n", target)
			return nil
		},
	}
	return cmd
}

func setup(ctx context.Context) (*client.Client, func(), error) {
	cfg, ok := config.FromContext(ctx)
	if !ok {
		return nil, func() {}, fmt.Errorf("configuration not loaded")
	}
	return bootstrap.SetupClient(ctx, bootstrap.Options{Config: cfg})
}
