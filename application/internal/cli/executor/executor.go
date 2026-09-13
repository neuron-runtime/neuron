package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/executor"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/progress"
	"github.com/Muhammad-Jay/neuron/application/internal/executorctl"
	"github.com/spf13/cobra"
)

// splitRef parses "name@version" into (name, version). Version is optional.
func splitRef(ref string) (typ, version string, err error) {
	typ, version, _ = strings.Cut(strings.TrimSpace(ref), "@")
	typ = strings.TrimSpace(typ)
	version = strings.TrimSpace(version)
	if typ == "" {
		return "", "", fmt.Errorf("executor name is required (e.g. %s)", command.Add)
	}
	return typ, version, nil
}

// catalogFromConfig builds the executorctl.Catalog from the loaded config.
func catalogFromConfig(ctx context.Context) (*executorctl.Catalog, error) {
	return catalogFromConfigObserver(ctx, nil)
}

// catalogFromConfigObserver builds the executorctl.Catalog from the loaded
// config, attaching an observer that receives resolution/installation
// progress events (may be nil).
func catalogFromConfigObserver(ctx context.Context, observer executor.Observer) (*executorctl.Catalog, error) {
	cfg, ok := config.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("configuration not loaded")
	}
	return executorctl.BuildCatalog(executorctl.CatalogConfig{
		ExecutorsConfig: cfg.Executors,
		Observer:        observer,
	})
}

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Executor,
		Short: "Manage executor packages",
		Long: `Resolve, install, and inspect executor packages in the local store.

Executors are referenced by logical name (e.g. github:read). They are resolved
against the configured registries (github, local) and installed immutably under
~/.neuron/executors.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newListCmd(),
		newInspectCmd(),
	)

	return cmd
}

// NewAddCmd returns the top-level `neuron add` command, which resolves and
// installs an executor package.
func NewAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Add,
		Short: "Resolve and install an executor package",
		Long: `Resolve and install an executor package into the local store.

	name@version selects an exact or constrained version (e.g. github:read or
	Muhammad-Jay:github:read@^1.0.0). Without a version the best matching
	version is installed.
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			rep := progress.New(cmd.ErrOrStderr())
			defer rep.Stop()

			catalog, err := catalogFromConfigObserver(ctx, rep)
			if err != nil {
				return err
			}

			typ, version, err := splitRef(args[0])
			if err != nil {
				return err
			}

			installed, err := catalog.Install(ctx, typ, version, nil)
			if err != nil {
				return err
			}

			fmt.Printf("installed %s@%s (from %s)\n", installed.Type, installed.Version, installed.Registry)
			return nil
		},
	}
	return cmd
}

func newListCmd() *cobra.Command {
	var typ string

	cmd := &cobra.Command{
		Use:   command.ExecutorList,
		Short: "List installed executors",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			catalog, err := catalogFromConfig(ctx)
			if err != nil {
				return err
			}

			installed, err := catalog.List(ctx, typ)
			if err != nil {
				return err
			}

			if len(installed) == 0 {
				fmt.Println("no executors installed")
				return nil
			}

			printList(installed)
			return nil
		},
	}
	cmd.Flags().StringVarP(&typ, "type", "t", "", "filter by executor type")
	return cmd
}

func newInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.ExecutorInspect,
		Short: "Inspect an installed executor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			catalog, err := catalogFromConfig(ctx)
			if err != nil {
				return err
			}

			typ, version, err := splitRef(args[0])
			if err != nil {
				return err
			}
			if version == "" {
				// Show the newest installed version for the type.
				list, err := catalog.List(ctx, typ)
				if err != nil {
					return err
				}
				if len(list) == 0 {
					return fmt.Errorf("executor %s is not installed", typ)
				}
				version = list[0].Version
			}

			installed, err := catalog.Inspect(ctx, typ, version)
			if err != nil {
				return err
			}

			printInspect(installed)
			return nil
		},
	}
	return cmd
}

// NewRemoveCmd returns the top-level `neuron remove` command, which removes an
// installed executor package.
func NewRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Remove,
		Short: "Remove an installed executor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			catalog, err := catalogFromConfig(ctx)
			if err != nil {
				return err
			}

			typ, version, err := splitRef(args[0])
			if err != nil {
				return err
			}
			if version == "" {
				return fmt.Errorf("version is required to remove (e.g. %s@%s)", typ, "1.2.0")
			}

			if err := catalog.Remove(ctx, typ, version); err != nil {
				return err
			}

			fmt.Printf("removed %s@%s\n", typ, version)
			return nil
		},
	}
	return cmd
}
