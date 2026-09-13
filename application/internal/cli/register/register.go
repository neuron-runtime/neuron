// Package register implements the deprecated `neuron register` command. It is
// an alias of `neuron build`, kept so existing workflows, scripts, and
// documentation keep working during the rename. The alias prints a deprecation
// notice and delegates to the `build` handler unchanged.
package register

import (
	"fmt"

	"github.com/Muhammad-Jay/neuron/application/internal/cli/build"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/spf13/cobra"
)

// New returns the deprecated `neuron register` command, an alias of
// `neuron build`.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Register,
		Short: "Deprecated: use `neuron build`",
		Long: "Build the project for the given authoring language, compile the resulting " +
			".neuron/manifest.json to a core.System, resolve and freeze the exact executor set, " +
			"and register the compiled system with N.O.R.E. " +
			"\n\nDeprecated: this command is an alias of `neuron build` and will be removed.",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.ErrOrStderr(), "neuron: `neuron register` is deprecated, use `neuron build`")
			return build.Handler(cmd, args)
		},
	}

	f := cmd.Flags()
	f.StringP("lang", "l", "", "project authoring language (yaml, yml, typescript, ts)")
	f.StringP("root", "r", "", "project root (defaults to the current directory)")

	return cmd
}
