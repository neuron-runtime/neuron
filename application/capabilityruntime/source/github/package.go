package github

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Muhammad-Jay/neuron/application/capabilityruntime"
	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

// manifestAssetName is the preferred runtime.json location inside a release.
const manifestAssetName = "runtime.json"

// packageArchiveAsset finds the capability runtime package archive asset in a release.
// The exact <type>-<version>-capabilityruntime.neuron.tar.gz name wins; otherwise any
// asset carrying the canonical package archive suffix is accepted, trusting
// the manifest inside the archive to reconcile identity at install time.
func packageArchiveAsset(release *Release, typ, version string) (Asset, bool) {
	if asset, ok := release.assetByName(capabilityrt.PackageArchiveName(typ, version)); ok {
		return asset, true
	}
	for _, a := range release.Assets {
		if strings.HasSuffix(a.Name, capabilityrt.PackageArchiveSuffix) {
			return a, true
		}
	}
	return Asset{}, false
}

// buildPackage assembles an capabilityruntime.Package for one release. The capability runtime
// package archive (<type>-<version>-capabilityruntime.neuron.tar.gz) is the preferred
// distribution: one immutable asset containing runtime.json plus every
// platform artifact. Its inner manifest is authoritative, so no separate
// manifest fetch happens here and identity/version are reconciled at install
// time. When no package archive exists, the manifest is read from the release
// (asset, then raw repository file) and the platform artifact for the
// capability runtime's runtime kind is bound.
func (r *Registry) buildPackage(
	ctx context.Context,
	typ, version string,
	ref RepoRef,
	release *Release,
) (*capabilityruntime.Package, error) {

	// Package archive distribution.
	if asset, ok := packageArchiveAsset(release, typ, version); ok {
		return &capabilityruntime.Package{
			Type:     typ,
			Version:  version,
			Registry: r.Name(),
			Artifact: capabilityruntime.Artifact{
				URL:  asset.BrowserDownloadURL,
				Name: asset.Name,
			},
		}, nil
	}

	// Legacy per-platform distribution.
	manifestBytes, source, err := r.fetchManifest(ctx, ref, release)
	if err != nil {
		return nil, err
	}
	if len(manifestBytes) == 0 {
		return nil, fmt.Errorf(
			"capability runtime %s@%s: runtime.json not found in release %s or repository",
			typ, version, ref.Owner+"/"+ref.Repo,
		)
	}

	m, err := capabilityruntime.ParseManifest(manifestBytes)
	if err != nil {
		return nil, err
	}

	// The release must actually serve this capability runtime's version.
	if v := strings.TrimPrefix(m.Metadata.Version, "v"); v != "" && v != version {
		return nil, fmt.Errorf(
			"capability runtime %s: manifest version %s does not match release version %s (release %s)",
			typ, m.Metadata.Version, version, source,
		)
	}

	pkg := capabilityruntime.PackageFromManifest(m, r.Name(), version)
	pkg.Manifest = manifestBytes

	// Bind the platform artifact for the capability runtime's runtime kind. WASM modules
	// select the "wasm32-wasi" key; process capability runtimes select the host key.
	platformKey := capabilityruntime.PlatformForRuntime(m.Runtime.Type)
	if platform, ok := m.Platforms[platformKey]; ok && platform.Artifact != "" {
		if asset, found := release.assetByName(platform.Artifact); found {
			pkg.Artifact = capabilityruntime.Artifact{
				URL:    asset.BrowserDownloadURL,
				Name:   asset.Name,
				SHA256: platform.SHA256,
			}
		} else {
			// Fall back to raw URL construction; the manifest is authoritative
			// for the artifact name even when the asset listing lags.
			pkg.Artifact = capabilityruntime.Artifact{
				URL:    rawDownloadURL(ref.Owner, ref.Repo, release.TagName, platform.Artifact),
				Name:   platform.Artifact,
				SHA256: platform.SHA256,
			}
		}
	}

	return pkg, nil
}

// fetchManifest returns the validated runtime.json bytes. Precedence: release
// asset named runtime.json, then the raw repository file at the release tag.
func (r *Registry) fetchManifest(ctx context.Context, ref RepoRef, release *Release) ([]byte, string, error) {
	if asset, ok := release.assetByName(manifestAssetName); ok {
		data, err := r.client.Get(ctx, asset.BrowserDownloadURL)
		if err != nil {
			return nil, "", err
		}
		return data, "asset " + asset.Name, nil
	}

	raw := rawFileURL(ref.Owner, ref.Repo, release.TagName, manifestAssetName)
	data, err := r.client.Get(ctx, raw)
	if err != nil {
		if errors.Is(err, notFoundSentinel) {
			return nil, "", nil
		}
		return nil, "", err
	}
	return data, "raw " + raw, nil
}

func rawFileURL(owner, repo, tag, file string) string {
	return "https://raw.githubusercontent.com/" + escapePath(owner) + "/" + escapePath(repo) + "/" + escapePath(tag) + "/" + escapePath(file)
}

func rawDownloadURL(owner, repo, tag, asset string) string {
	return "https://github.com/" + escapePath(owner) + "/" + escapePath(repo) + "/releases/download/" + escapePath(tag) + "/" + escapePath(asset)
}
