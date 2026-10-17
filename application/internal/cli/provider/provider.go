// Package provider implements `neuron provider`, the development and
// bootstrap surface for first-party provider artifacts. Providers obtained
// from a registry use `neuron install --provider`; this command exists so a
// development environment can obtain the built-in provider identities
// without any registry, network, or fabricated executable payload.
package provider

import (
	"fmt"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/internal/providerctl"
	providerpkg "github.com/neuron-runtime/neuron/application/provider"
	"github.com/spf13/cobra"
)

// New returns the top-level `neuron provider` command.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Provider development and bootstrap",
		Long: `Manage first-party provider artifacts for local development.

Providers resolved from a registry are installed with
'neuron install --provider'. The 'provider build' subcommand is a local
bootstrap mechanism that performs no registry resolution.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newBuildCmd())
	return cmd
}

func newBuildCmd() *cobra.Command {
	var outDir string
	var version string

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Bootstrap descriptor-only first-party providers into the store",
		Long: `Build the descriptor-only first-party provider artifacts (process, wasm) and
install them into the provider store through the real installer.

This is a local development/bootstrap mechanism, not a registry install. It
performs no registry resolution and no download, and the artifacts carry no
executable payload: each one is a valid provider.runtime.json identity that
project builds and invocations resolve exactly like a registry-installed
provider. The install record marks every artifact with the bootstrap source
so it is never mistaken for a registry install.

The build is deterministic and repeatable: identical inputs produce the same
content digest, and rebuilding over an existing identical artifact reports
'already present'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			cfg, ok := config.FromContext(ctx)
			if !ok {
				return fmt.Errorf("configuration not loaded")
			}
			if outDir != "" {
				cfg.Providers.StoreDir = outDir
			}

			catalog, err := providerctl.BuildCatalog(providerctl.CatalogConfig{
				ProvidersConfig: cfg.Providers,
			})
			if err != nil {
				return err
			}

			results, err := providerpkg.Bootstrap(ctx, catalog.Installer, version)
			if err != nil {
				return err
			}
			for _, res := range results {
				digest := res.Digest
				if len(digest) > 16 {
					digest = digest[:16]
				}
				if res.AlreadyPresent {
					fmt.Printf("bootstrap: %s@%s already present (%s)\n", res.Name, res.Version, digest)
				} else {
					fmt.Printf("bootstrap: %s@%s (%s)\n", res.Name, res.Version, digest)
				}
			}
			fmt.Printf("bootstrap: providers available in %s\n", catalog.StoreDir())
			return nil
		},
	}

	cmd.Flags().StringVarP(&outDir, "out", "o", "", "override the target provider store root (defaults to providers.storeDir)")
	cmd.Flags().StringVar(&version, "provider-version", providerpkg.FirstPartyVersion, "version assigned to the bootstrapped providers")
	return cmd
}
