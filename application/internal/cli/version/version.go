package version

import (
	"fmt"

	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	neuronversion "github.com/neuron-runtime/neuron/shared/version"
	"github.com/spf13/cobra"
)

func New() *cobra.Command {
	return &cobra.Command{
		Use:   command.Version,
		Short: "Print the Neuron CLI version",
		Long:  "Print the version of the Neuron CLI and the N.O.R.E. runtime it manages.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("neuron %s\n", neuronversion.String())
			return nil
		},
	}
}
