// Package installcmd implements `neuron install`, the explicit command that
// resolves and installs a provider artifact into the machine-global provider
// store. It is deliberately separate from `neuron add`: `add` pins a
// capability runtime into the project, while `install --provider` installs a
// host-level provider artifact that Assemblies resolve against at build time.
package installcmd

import (
	"fmt"
	"strings"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/internal/providerctl"
	"github.com/neuron-runtime/neuron/application/internal/ref"
	"github.com/spf13/cobra"
)

// splitRef parses "name@version" into (name, version). Version is optional
// and may be an exact pin ("1.0.0") or a constraint ("^1.0.0").
func splitRef(s string) (name, version string, err error) {
	name, version, ok := ref.Parse(s)
	if !ok {
		return "", "", fmt.Errorf("provider name is required (e.g. process)")
	}
	return name, version, nil
}

// New returns the top-level `neuron install` command.
func New() *cobra.Command {
	var providerRef string
	var registries []string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install a provider artifact",
		Long: `Resolve, verify, and install a provider artifact into the machine-global provider
store (~/.neuron/providers).

Providers are host-level execution artifacts (process, wasm, ...). They are
distinct from capability runtime packages, which are installed with
'neuron add' and pinned into the project.

 name selects the best available version; name@version pins an exact
 version; name@constraint (e.g. ^1.0.0) selects the best matching one.
 Providers are resolved against the configured registries (github, local)
 and installed immutably, verified by content digest.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if strings.TrimSpace(providerRef) == "" {
				return fmt.Errorf("specify a provider with --provider (e.g. --provider process@1.0.0); capability runtimes are installed with `neuron add`")
			}

			cfg, ok := config.FromContext(ctx)
			if !ok {
				return fmt.Errorf("configuration not loaded")
			}

			catalog, err := providerctl.BuildCatalog(providerctl.CatalogConfig{
				ProvidersConfig: cfg.Providers,
			})
			if err != nil {
				return err
			}

			name, version, err := splitRef(providerRef)
			if err != nil {
				return err
			}

			installed, err := catalog.Install(ctx, name, version, registries)
			if err != nil {
				return err
			}

			digest := installed.Digest
			if len(digest) > 16 {
				digest = digest[:16]
			}
			fmt.Printf("installed %s@%s (%s, from %s)\n", installed.Name, installed.Version, digest, installed.Registry)
			return nil
		},
	}

	cmd.Flags().StringVar(&providerRef, "provider", "", "provider reference: name, name@version, or name@constraint")
	cmd.Flags().StringSliceVar(&registries, "registry", nil, "registries allowed to satisfy the requirement (default: configured providers.defaultRegistries)")

	return cmd
}
