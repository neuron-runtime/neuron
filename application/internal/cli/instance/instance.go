package instance

import (
	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/spf13/cobra"
)

var instanceID string

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Instance,
		Short: "Manage N.O.R.E. systems instances",
		Long:  `Create, list, and manage running systems instances.`,
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
