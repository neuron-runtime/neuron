package utils

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Muhammad-Jay/neuron/application/capabilityruntime"
	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/language"
	"github.com/Muhammad-Jay/neuron/application/project"
	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

// AuthoringInputs assembles the project's authoring inputs for a build
// fingerprint: the effective config file, the Assembly entry source, and every
// project-owned source tree (capabilities/, implicit capability runtime root, configured
// localRoots, fileassembly-backed local registries).
//
// Only inputs that exist participate; a pristine project with no capabilities/
// directory and no capability runtime roots fingerprints its config and entry alone.
// Local capability runtime declared build outputs (runtime.json artifact.path) are
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
			entry = "assembly.yaml"
		} else {
			entry = "index.ts"
		}
	}
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(root, entry)
	}
	in.Entry = entry

	localRoots := []string{}
	if dirExists(filepath.Join(root, filepath.FromSlash(project.ImplicitCapabilityRuntimeRoot))) {
		localRoots = append(localRoots, filepath.Join(root, filepath.FromSlash(project.ImplicitCapabilityRuntimeRoot)))
	}
	localRoots = append(localRoots, cfg.CapabilityRuntimes.LocalRoots...)

	// Local registries with a fileassembly URL are authoring inputs too.
	for _, reg := range cfg.CapabilityRuntimes.Registries {
		if reg.Name != "local" || reg.URL == "" || reg.URL == "local://" {
			continue
		}
		if dirExists(reg.URL) {
			localRoots = append(localRoots, reg.URL)
		}
	}

	excluded, err := capabilityRuntimeBuildOutputs(localRoots)
	if err != nil {
		return in, err
	}
	in.Exclude = excluded

	for _, r := range localRoots {
		in.Sources = append(in.Sources, r)
	}

	capabilitiesDir := filepath.Join(root, "capabilities")
	if dirExists(capabilitiesDir) {
		in.Sources = append(in.Sources, capabilitiesDir)
	}

	return in, nil
}

// capabilityRuntimeBuildOutputs returns every artifact a local capability runtime build command is
// declared to produce (runtime.json artifact.path under the given roots).
// These are build outputs, not authoring inputs, so they must not participate
// in the fingerprint.
func capabilityRuntimeBuildOutputs(roots []string) ([]string, error) {
	var out []string
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || info.Name() != capabilityrt.ManifestFile {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("read %s: %w", path, readErr)
			}
			m, parseErr := capabilityruntime.ParseManifest(data)
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
			return nil, fmt.Errorf("scan capability runtime outputs in %s: %w", root, err)
		}
	}
	return out, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
