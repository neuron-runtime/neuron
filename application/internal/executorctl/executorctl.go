// Package executorctl assembles the executor registry pipeline from the
// application config and provides the user-facing executor operations used by
// the `neuron executor` CLI and the register flow.
package executorctl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/executor"
	"github.com/Muhammad-Jay/neuron/application/executor/source/github"
	"github.com/Muhammad-Jay/neuron/application/executor/source/local"
	"github.com/Muhammad-Jay/neuron/application/executor/store"
	"github.com/Muhammad-Jay/neuron/application/project"
)

// Catalog bundles the wired executor pipeline.
type Catalog struct {
	cfg        config.ExecutorsConfig
	roots      []string
	Registry   *executor.Registry
	Store      executor.Store
	Installer  *executor.Installer
	Downloader executor.Downloader
	Resolver   *executor.Resolver
}

// CatalogConfig adapts a config into the pipeline dependencies.
type CatalogConfig struct {
	config.ExecutorsConfig

	// ProjectRoot is the project root. The project's own ./neuron/executors
	// directory is always an implicit local executor root. When empty the
	// current working directory is used.
	ProjectRoot string

	// Observer receives resolution/installation progress events. When nil,
	// the pipeline runs silently.
	Observer executor.Observer

	// GitHubToken overrides the token discovered from the environment.
	GitHubToken string

	// GitHubCatalog optionally overrides type -> owner/repo resolution.
	GitHubCatalog map[string]github.RepoRef
}

// buildCatalog creates a fully wired executor pipeline.
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

	// Register project-local executor roots as one "local" registry. The
	// project's own ./neuron/executors directory is always an implicit root;
	// configured localRoots and `local` registries add more. Roots that were
	// explicitly configured but do not exist are a configuration error.
	origins, err := collectLocalRoots(cfg.ExecutorsConfig, cfg.ProjectRoot)
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, origin := range origins {
		if !dirExists(origin.dir) {
			if origin.implicit {
				// A pristine project may not have created the directory yet.
				continue
			}
			return nil, fmt.Errorf("configured local executor root %q does not exist", origin.dir)
		}
		roots = append(roots, origin.dir)
	}
	if len(roots) > 0 {
		loc, err := local.NewMulti(roots...)
		if err != nil {
			return nil, fmt.Errorf("open local executor roots: %w", err)
		}
		if err := reg.Add(loc); err != nil {
			return nil, err
		}
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
			// Local roots are registered above; this keeps the switch
			// explicit and leaves room for local://-style URLs later.
		default:
			return nil, fmt.Errorf("unknown executor registry %q (supported: github, local)", r.Name)
		}
	}

	downloader := executor.NewHTTPDownloader()
	installer := &executor.Installer{Store: fsStore, Downloader: downloader, Observer: cfg.Observer}
	resolver := executor.NewResolver(reg, fsStore, installer)
	resolver.Observer = cfg.Observer

	return &Catalog{
		cfg:        cfg.ExecutorsConfig,
		roots:      roots,
		Registry:   reg,
		Store:      fsStore,
		Installer:  installer,
		Downloader: downloader,
		Resolver:   resolver,
	}, nil
}

// Roots returns the absolute local executor search roots backing this catalog
// (the implicit project root plus any configured localRoots/local registries).
func (c *Catalog) Roots() []string {
	out := make([]string, len(c.roots))
	copy(out, c.roots)
	return out
}

// localOrigin is one local executor search root. implicit roots (the project's
// own ./neuron/executors) may legitimately not exist yet; explicitly
// configured roots must.
type localOrigin struct {
	dir      string
	implicit bool
}

// collectLocalRoots computes the deduplicated, absolute set of local executor
// search roots: the implicit project root, configured localRoots, and any
// `local` registry entries.
func collectLocalRoots(ec config.ExecutorsConfig, projectRoot string) ([]localOrigin, error) {
	base := projectRoot
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve project root: %w", err)
		}
		base = cwd
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("resolve project root %q: %w", base, err)
	}
	absBase = filepath.Clean(absBase)

	seen := make(map[string]bool)
	var out []localOrigin

	add := func(dir string, implicit bool) error {
		if strings.TrimSpace(dir) == "" {
			return nil
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolve local executor root %q: %w", dir, err)
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			return nil
		}
		seen[abs] = true
		out = append(out, localOrigin{dir: abs, implicit: implicit})
		return nil
	}

	// The project's own ./neuron/executors is always a search root.
	if err := add(filepath.Join(absBase, filepath.FromSlash(project.ImplicitExecutorRoot)), true); err != nil {
		return nil, err
	}

	for _, root := range ec.LocalRoots {
		if err := add(root, false); err != nil {
			return nil, err
		}
	}

	for _, r := range ec.Registries {
		if r.Name != "local" {
			continue
		}
		if r.URL == "" || r.URL == "local://" {
			continue
		}
		if err := add(r.URL, false); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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

// InstallForce is Install with the store fast-path disabled: the registries
// are always consulted and the selected version is freshly fetched and
// verified even when an identical version is already installed.
func (c *Catalog) InstallForce(ctx context.Context, typ, version string, registries []string) (*executor.Installed, error) {
	req := c.Require(typ, version, registries)
	return c.Resolver.ResolveForce(ctx, req)
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
