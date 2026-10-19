package instance

import (
	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/spf13/cobra"
)

var instanceID string

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Instance,
		Short: "Manage Assembly instances and their executions",
		Long:  `Advanced: list and manage running Assembly instances and inspect their executions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newListCmd(),
		newRemoveCmd(),
		newClearCmd(),
	)

	return cmd
}
