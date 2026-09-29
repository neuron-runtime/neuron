package instance

import (
	"fmt"

	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/spf13/cobra"
)

func newClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.InstanceClear,
		Short: "Remove all instances and their executions and events",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			c, cleanup, err := setup(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := c.ClearInstances(ctx); err != nil {
				return fmt.Errorf("clear instances: %w", err)
			}

			fmt.Println("removed all instances")
			return nil
		},
	}
	return cmd
}
