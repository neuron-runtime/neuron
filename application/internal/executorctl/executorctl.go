// Package executorctl assembles the executor registry pipeline from the
// application config and provides the user-facing executor operations used by
// the `neuron executor` CLI and the register flow.
package executorctl

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/executor"
	"github.com/Muhammad-Jay/neuron/application/executor/source/github"
	"github.com/Muhammad-Jay/neuron/application/executor/source/local"
	"github.com/Muhammad-Jay/neuron/application/executor/store"
)

// Catalog bundles the wired executor pipeline.
type Catalog struct {
	cfg        config.ExecutorsConfig
	Registry   *executor.Registry
	Store      executor.Store
	Installer  *executor.Installer
	Downloader executor.Downloader
	Resolver   *executor.Resolver
}

// CatalogConfig adapts a config into the pipeline dependencies.
type CatalogConfig struct {
	config.ExecutorsConfig

	// Observer receives resolution/installation progress events. When nil,
	// the pipeline runs silently.
	Observer executor.Observer

	// GitHubToken overrides the token discovered from the environment.
	GitHubToken string

	// GitHubCatalog optionally overrides type -> owner/repo resolution.
	GitHubCatalog map[string]github.RepoRef
}

// BuildCatalog creates a fully wired executor pipeline.
func BuildCatalog(cfg CatalogConfig) (*Catalog, error) {
	storeDir := cfg.ExecutorsConfig.StoreDir
	if storeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home for executor store: %w", err)
		}
		storeDir = store.DefaultStoreDir()
		if storeDir == "" {
			storeDir = home + "/.neuron/executors"
		}
	}

	fsStore, err := store.NewFilesystemStore(storeDir)
	if err != nil {
		return nil, err
	}

	reg := executor.NewRegistry()

	token := cfg.GitHubToken
	if token == "" {
		token = os.Getenv("NEURON_GITHUB_TOKEN")
	}

	for _, r := range cfg.Registries {
		switch r.Name {
		case "github":
			gh := github.New(github.WithToken(token))
			if cfg.GitHubCatalog != nil {
				gh = github.New(github.WithToken(token), github.WithCatalog(cfg.GitHubCatalog))
			}
			if err := reg.Add(gh); err != nil {
				return nil, err
			}
		case "local":
			if r.URL != "" && r.URL != "local://" {
				loc, err := local.New(r.URL)
				if err == nil {
					if err := reg.Add(loc); err != nil {
						return nil, err
					}
				}
			}
		}
	}

	downloader := executor.NewHTTPDownloader()
	installer := &executor.Installer{Store: fsStore, Downloader: downloader, Observer: cfg.Observer}
	resolver := executor.NewResolver(reg, fsStore, installer)
	resolver.Observer = cfg.Observer

	return &Catalog{
		cfg:        cfg.ExecutorsConfig,
		Registry:   reg,
		Store:      fsStore,
		Installer:  installer,
		Downloader: downloader,
		Resolver:   resolver,
	}, nil
}

// Require converts an explicit requirement into the resolver-facing value.
// registries defaults to the configured DefaultRegistries when empty.
func (c *Catalog) Require(typ, version string, registries []string) executor.Requirement {
	req := executor.Requirement{
		Type:    typ,
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

// Resolve resolves the requirements and freezes them for a Deployment.
func (c *Catalog) Resolve(ctx context.Context, requirements []executor.Requirement) (*executor.Environment, error) {
	return c.Resolver.ResolveMany(ctx, requirements)
}

// Install installs an explicit executor spec (name, optional version).
func (c *Catalog) Install(ctx context.Context, typ, version string, registries []string) (*executor.Installed, error) {
	req := c.Require(typ, version, registries)
	return c.Resolver.Resolve(ctx, req)
}

// List returns every installed version of typ (or all when typ is empty).
func (c *Catalog) List(ctx context.Context, typ string) ([]executor.Installed, error) {
	return c.Store.List(ctx, typ)
}

// Inspect returns the installed record for name@version.
func (c *Catalog) Inspect(ctx context.Context, typ, version string) (*executor.Installed, error) {
	installed, err := c.Store.Get(ctx, typ, version)
	if err != nil {
		return nil, err
	}
	return installed, nil
}

// Remove deletes an installed record.
func (c *Catalog) Remove(ctx context.Context, typ, version string) error {
	return c.Store.Remove(ctx, typ, version)
}

// StoreDir returns the effective store root.
func (c *Catalog) StoreDir() string {
	return c.Store.Root()
}
