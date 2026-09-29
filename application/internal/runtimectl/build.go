package runtimectl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/neuron-runtime/neuron/application/capabilityruntime/source/local"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// BuildOptions controls how BuildLocal materializes local capability runtimes.
type BuildOptions struct {
	// MaxWorkers bounds how many capability runtime build commands run concurrently.
	// Zero means one at a time (the safe default: builds share the project).
	MaxWorkers int

	// Force rebuilds every capability runtime and re-stages it even when the store
	// already holds a fresh copy.
	Force bool

	// ProjectRoot is exported to build commands as NEURON_PROJECT_ROOT so a
	// package can reference sibling source in its monorepo.
	ProjectRoot string

	// Status receives one line per capability runtime decision (cached / built /
	// installed / error). Used by the CLI to render aggregate progress.
	Status func(typ, version, message string)
}

// localJob pairs a local search root with one discovered version directory.
type localJob struct {
	root string
	spec *local.VersionSpec
}

// BuildLocal materializes every capability runtime under the catalog's local roots into
// the store. CapabilityRuntimes whose payload is missing run their declared
// build.command first (in the version directory), then the produced payload
// is staged and installed. CapabilityRuntimes already installed are skipped with a
// "using cached version" note unless Force is set.
//
// A local capability runtime with neither an existing payload nor a build.command is a
// hard error naming the capability runtime; builds never silently skip a broken
// definition.
func (c *Catalog) BuildLocal(ctx context.Context, opts BuildOptions) error {
	if opts.MaxWorkers < 1 {
		opts.MaxWorkers = 1
	}

	jobs, err := collectLocalJobs(c.Roots())
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return nil
	}

	sem := make(chan struct{}, opts.MaxWorkers)
	var wg sync.WaitGroup
	mu := sync.Mutex{}
	var firstErr error

	for _, job := range jobs {
		wg.Add(1)
		go func(job localJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := c.ensureLocalCapabilityRuntime(ctx, job, opts); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				notifyStatus(opts.Status, job.spec.Type, job.spec.Version, "failed: "+err.Error())
			}
		}(job)
	}

	wg.Wait()
	return firstErr
}

// ensureLocalCapabilityRuntime makes sure job's version dir is buildable and installed.
func (c *Catalog) ensureLocalCapabilityRuntime(ctx context.Context, job localJob, opts BuildOptions) error {
	spec := job.spec

	if !opts.Force {
		if installed, err := c.Store.Get(ctx, spec.Type, spec.Version); err == nil && installed != nil {
			notifyStatus(opts.Status, spec.Type, spec.Version, "using cached version")
			return nil
		}
	}

	if !payloadExists(spec.Artifact) {
		if spec.BuildCommand == "" {
			return fmt.Errorf("capability runtime %s@%s has no artifact to install and no build.command", spec.Type, spec.Version)
		}
		notifyStatus(opts.Status, spec.Type, spec.Version, "building")

		if err := runCapabilityRuntimeBuild(ctx, spec, opts.ProjectRoot); err != nil {
			return fmt.Errorf("build capability runtime %s@%s: %w", spec.Type, spec.Version, err)
		}

		// Re-discover: the build should have produced the payload. It may
		// legitimately change what payload is resolved (e.g. build emitted a
		// canonical archive when artifact.path pointed at a directory).
		red, err := local.DiscoverVersion(job.root, spec.Type, spec.Version)
		if err != nil {
			return fmt.Errorf("capability runtime %s@%s: after build.command the payload is unresolvable: %w", spec.Type, spec.Version, err)
		}
		spec = red

		if !payloadExists(spec.Artifact) {
			hint := fmt.Sprintf("artifact %q was not produced", spec.Artifact)
			if spec.Manifest != nil && spec.Manifest.Artifact != nil && spec.Manifest.Artifact.Path != "" {
				hint = fmt.Sprintf("artifact.path %q was not produced by the build command", spec.Manifest.Artifact.Path)
			}
			return fmt.Errorf("build capability runtime %s@%s: command ran but %s", spec.Type, spec.Version, hint)
		}
	}

	if opts.Force {
		if err := c.Store.Remove(ctx, spec.Type, spec.Version); err != nil && !isStoreNotFound(err) {
			return fmt.Errorf("force-reinstall capability runtime %s@%s: %w", spec.Type, spec.Version, err)
		}
	}

	pkg := local.PackageFromSpec(spec)
	result, err := c.Installer.Install(ctx, pkg)
	if err != nil {
		return fmt.Errorf("install capability runtime %s@%s: %w", spec.Type, spec.Version, err)
	}
	if result.AlreadyPresent {
		notifyStatus(opts.Status, spec.Type, spec.Version, "using cached version")
		return nil
	}
	notifyStatus(opts.Status, spec.Type, spec.Version, "installed")
	return nil
}

// collectLocalJobs walks every local root for version directories and derives
// their logical (type, version) from the directory layout. Duplicate
// (type, version) pairs across roots collapse to the first root (implicit
// project root wins).
func collectLocalJobs(roots []string) ([]localJob, error) {
	var jobs []localJob
	seen := make(map[string]bool)

	for _, root := range roots {
		manifests := findLocalManifests(root)
		sort.Strings(manifests)
		for _, mpath := range manifests {
			rel, err := filepath.Rel(root, mpath)
			if err != nil {
				return nil, err
			}
			segments := strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/")
			if len(segments) < 2 {
				continue
			}
			version := segments[len(segments)-1]
			typ := strings.Join(segments[:len(segments)-1], ":")

			key := typ + "@" + version
			if seen[key] {
				continue
			}
			seen[key] = true

			spec, err := local.DiscoverVersion(root, typ, version)
			if err != nil {
				return nil, fmt.Errorf("discover capability runtime %s@%s: %w", typ, version, err)
			}
			jobs = append(jobs, localJob{root: root, spec: spec})
		}
	}
	return jobs, nil
}

// findLocalManifests walks root and returns every runtime.json path that sits
// at least two directory levels deep (owner/.../version/runtime.json).
func findLocalManifests(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Name() != capabilityrt.ManifestFile {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out
}

// payloadExists reports whether path names a file or directory on disk.
func payloadExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// runCapabilityRuntimeBuild executes the manifest build.command in the version
// directory, exporting NEURON_PROJECT_ROOT and NEURON_EXECUTOR_DIR. The
// command is run through the platform shell and inherits the parent
// environment.
func runCapabilityRuntimeBuild(ctx context.Context, spec *local.VersionSpec, projectRoot string) error {
	full := spec.BuildCommand
	dir := spec.Root

	env := append(os.Environ(),
		"NEURON_PROJECT_ROOT="+projectRoot,
		"NEURON_EXECUTOR_DIR="+dir,
	)

	var shell, flag string
	if runtime.GOOS == "windows" {
		shell, flag = "cmd", "/C"
	} else {
		shell, flag = "/bin/sh", "-c"
	}

	c := exec.CommandContext(ctx, shell, flag, full)
	c.Dir = dir
	c.Env = env
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	return c.Run()
}

// isStoreNotFound reports whether err is a store miss (delete of a missing
// capability runtime is treated as idempotent).
func isStoreNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

func notifyStatus(fn func(string, string, string), typ, version, msg string) {
	if fn != nil {
		fn(typ, version, msg)
	}
}
