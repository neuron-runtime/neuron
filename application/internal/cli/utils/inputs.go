package utils

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/executor"
	"github.com/Muhammad-Jay/neuron/application/language"
	"github.com/Muhammad-Jay/neuron/application/project"
	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
)

// AuthoringInputs assembles the project's authoring inputs for a build
// fingerprint: the effective config file, the System entry source, and every
// project-owned source tree (services/, implicit executor root, configured
// localRoots, filesystem-backed local registries).
//
// Only inputs that exist participate; a pristine project with no services/
// directory and no executor roots fingerprints its config and entry alone.
// Local-executor declared build outputs (executor.json artifact.path) are
// excluded so in-place builds never churn the fingerprint.
func AuthoringInputs(root string, cfg config.Config) (project.FingerprintInputs, error) {
	in := project.FingerprintInputs{
		ConfigFile: cfg.ConfigFile,
	}

	entry := cfg.Entry
	if entry == "" {
		lang, err := language.Resolve("", cfg.Lang, root)
		if err != nil {
			return in, err
		}
		if lang.Is(language.YAML) {
			entry = "system.yaml"
		} else {
			entry = "index.ts"
		}
	}
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(root, entry)
	}
	in.Entry = entry

	localRoots := []string{}
	if dirExists(filepath.Join(root, filepath.FromSlash(project.ImplicitExecutorRoot))) {
		localRoots = append(localRoots, filepath.Join(root, filepath.FromSlash(project.ImplicitExecutorRoot)))
	}
	localRoots = append(localRoots, cfg.Executors.LocalRoots...)

	// Local registries with a filesystem URL are authoring inputs too.
	for _, reg := range cfg.Executors.Registries {
		if reg.Name != "local" || reg.URL == "" || reg.URL == "local://" {
			continue
		}
		if dirExists(reg.URL) {
			localRoots = append(localRoots, reg.URL)
		}
	}

	excluded, err := executorBuildOutputs(localRoots)
	if err != nil {
		return in, err
	}
	in.Exclude = excluded

	for _, r := range localRoots {
		in.Sources = append(in.Sources, r)
	}

	servicesDir := filepath.Join(root, "services")
	if dirExists(servicesDir) {
		in.Sources = append(in.Sources, servicesDir)
	}

	return in, nil
}

// executorBuildOutputs returns every artifact a local executor build command is
// declared to produce (executor.json artifact.path under the given roots).
// These are build outputs, not authoring inputs, so they must not participate
// in the fingerprint.
func executorBuildOutputs(roots []string) ([]string, error) {
	var out []string
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || info.Name() != shadexec.ManifestFile {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("read %s: %w", path, readErr)
			}
			m, parseErr := executor.ParseManifest(data)
			if parseErr != nil {
				return fmt.Errorf("parse %s: %w", path, parseErr)
			}
			if m == nil || m.Artifact == nil || m.Artifact.Path == "" {
				return nil
			}
			abs := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(m.Artifact.Path)))
			out = append(out, abs)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan executor outputs in %s: %w", root, err)
		}
	}
	return out, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
