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
			force, err := cmd.Flags().GetBool("force")
			if err != nil {
				return err
			}

			c, cleanup, err := setup(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := c.ClearInstances(ctx, force); err != nil {
				return fmt.Errorf("clear instances: %w", err)
			}

			fmt.Println("removed all instances")
			return nil
		},
	}
	cmd.Flags().Bool("force", false, "clear instances even when they have unfinished executions")
	return cmd
}
