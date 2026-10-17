// Package providerctl assembles the provider registry pipeline from the
// application config and provides the user-facing provider operations used
// by the `neuron install --provider` CLI. It is the provider-side
// counterpart of the capability runtime runtimectl package.
package providerctl

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/internal/githubrepo"
	"github.com/neuron-runtime/neuron/application/provider"
	providergithub "github.com/neuron-runtime/neuron/application/provider/source/github"
	"github.com/neuron-runtime/neuron/application/provider/source/local"
	providerstore "github.com/neuron-runtime/neuron/application/provider/store"
)

// Catalog bundles the wired provider pipeline.
type Catalog struct {
	cfg       config.ProvidersConfig
	Registry  *provider.Registry
	Store     provider.Store
	Installer *provider.Installer
	Resolver  *provider.Resolver
}

// CatalogConfig adapts a config into the pipeline dependencies.
type CatalogConfig struct {
	config.ProvidersConfig

	// GitHubToken overrides the token discovered from the environment.
	GitHubToken string

	// GitHubCatalog overrides provider name → owner/repo resolution. The
	// GitHub source is catalog-driven; names absent from the catalog are
	// rejected rather than guessed.
	GitHubCatalog map[string]githubrepo.RepoRef
}

// BuildCatalog creates a fully wired provider pipeline. Unlike capability
// runtimes there is no implicit project root: providers are machine-global,
// so only explicitly configured localRoots are served.
func BuildCatalog(cfg CatalogConfig) (*Catalog, error) {
	storeDir := cfg.ProvidersConfig.StoreDir
	if strings.TrimSpace(storeDir) == "" {
		storeDir = config.DefaultProviderStoreDir()
	}

	fsStore, err := providerstore.NewFileSystemStore(storeDir)
	if err != nil {
		return nil, err
	}

	reg := provider.NewRegistry()

	token := cfg.GitHubToken
	if token == "" {
		token = os.Getenv("NEURON_GITHUB_TOKEN")
	}

	// Explicitly configured local roots form the "local" source. A root
	// that is configured but missing is a configuration error.
	if len(cfg.ProvidersConfig.LocalRoots) > 0 {
		roots := make([]string, 0, len(cfg.ProvidersConfig.LocalRoots))
		for _, root := range cfg.ProvidersConfig.LocalRoots {
			if strings.TrimSpace(root) == "" {
				continue
			}
			info, err := os.Stat(root)
			if err != nil || !info.IsDir() {
				return nil, fmt.Errorf("configured local provider root %q does not exist", root)
			}
			roots = append(roots, root)
		}
		if len(roots) > 0 {
			loc, err := local.New(roots...)
			if err != nil {
				return nil, fmt.Errorf("open local provider roots: %w", err)
			}
			if err := reg.Add(loc); err != nil {
				return nil, err
			}
		}
	}

	for _, r := range cfg.ProvidersConfig.Registries {
		switch r.Name {
		case "github":
			opts := []providergithub.Option{providergithub.WithToken(token)}
			if cfg.GitHubCatalog != nil {
				opts = append(opts, providergithub.WithCatalog(cfg.GitHubCatalog))
			}
			if err := reg.Add(providergithub.New(opts...)); err != nil {
				return nil, err
			}
		case "local":
			// Local roots are registered above; this keeps the switch
			// explicit.
		default:
			return nil, fmt.Errorf("unknown provider registry %q (supported: github, local)", r.Name)
		}
	}

	installer := &provider.Installer{
		Store:      fsStore,
		Downloader: provider.NewHTTPDownloader(),
	}
	resolver := provider.NewResolver(reg, fsStore, installer)

	return &Catalog{
		cfg:       cfg.ProvidersConfig,
		Registry:  reg,
		Store:     fsStore,
		Installer: installer,
		Resolver:  resolver,
	}, nil
}

// Require converts an explicit requirement into the resolver-facing value.
// Registries default to the configured DefaultRegistries when empty.
func (c *Catalog) Require(name, version string, registries []string) provider.Requirement {
	req := provider.Requirement{
		Name:    name,
		Version: version,
	}
	registries = nonEmptyRegistries(registries)
	if len(registries) == 0 {
		req.Registries = c.cfg.DefaultRegistries
	} else {
		req.Registries = registries
	}
	return req
}

func nonEmptyRegistries(registries []string) []string {
	if len(registries) == 0 {
		return nil
	}
	out := make([]string, 0, len(registries))
	for _, name := range registries {
		if strings.TrimSpace(name) != "" {
			out = append(out, name)
		}
	}
	return out
}

// Install resolves and installs an explicit provider spec (name, optional
// version or constraint).
func (c *Catalog) Install(ctx context.Context, name, version string, registries []string) (*provider.Installed, error) {
	return c.Resolver.Resolve(ctx, c.Require(name, version, registries))
}

// List returns every installed version of name (or all when name is empty).
func (c *Catalog) List(ctx context.Context, name string) ([]provider.Installed, error) {
	return c.Store.List(ctx, name)
}

// Inspect returns the installed record for name@version.
func (c *Catalog) Inspect(ctx context.Context, name, version string) (*provider.Installed, error) {
	return c.Store.Get(ctx, name, version)
}

// Remove deletes an installed version.
func (c *Catalog) Remove(ctx context.Context, name, version string) error {
	return c.Store.Remove(ctx, name, version)
}

// StoreDir returns the effective store root.
func (c *Catalog) StoreDir() string {
	return c.Store.Root()
}
