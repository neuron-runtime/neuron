package githubrepo

import (
	"context"
	"sort"
)

// RepoRef identifies a GitHub repository as owner/name.
type RepoRef struct {
	Owner string
	Repo  string
}

// Release is the subset of the GitHub Releases API record registries need.
type Release struct {
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}

// Asset is one downloadable release asset.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// ListReleases fetches published (non-draft) releases for a repository.
func (c *Client) ListReleases(ctx context.Context, owner, repo string) ([]Release, error) {
	endpoint := "repos/" + EscapePath(owner) + "/" + EscapePath(repo) + "/releases?per_page=100"

	var releases []Release
	if err := c.do(ctx, "GET", endpoint, &releases); err != nil {
		return nil, err
	}

	filtered := make([]Release, 0, len(releases))
	for _, r := range releases {
		if r.Draft {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered, nil
}

// ReleaseByTag fetches the release for an exact tag (e.g. "v1.2.0").
func (c *Client) ReleaseByTag(ctx context.Context, owner, repo, tag string) (*Release, error) {
	endpoint := "repos/" + EscapePath(owner) + "/" + EscapePath(repo) + "/releases/tags/" + EscapePath(tag)

	var release Release
	if err := c.do(ctx, "GET", endpoint, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// VersionsFromReleases extracts semver-ish versions from release tags. Only
// non-empty, valid version strings survive semver selection upstream, but
// prereleases are excluded here unless no stable release exists.
func VersionsFromReleases(releases []Release) []string {
	var stable []string
	var prerelease []string
	for _, r := range releases {
		v := ParseTagToVersion(r.TagName)
		if v == "" {
			continue
		}
		if r.Prerelease {
			prerelease = append(prerelease, v)
		} else {
			stable = append(stable, v)
		}
	}

	// Prefer stable releases; fall back to prereleases only when there is no
	// stable release, so floating requirements never silently grab alphas.
	versions := stable
	if len(versions) == 0 {
		versions = prerelease
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i] > versions[j]
	})
	return versions
}

// AssetByName finds a release asset by exact name.
func (r *Release) AssetByName(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// RawFileURL builds the raw.githubusercontent.com URL for a repository file
// at a given ref.
func RawFileURL(owner, repo, tag, file string) string {
	return "https://raw.githubusercontent.com/" + EscapePath(owner) + "/" + EscapePath(repo) + "/" + EscapePath(tag) + "/" + EscapePath(file)
}

// RawDownloadURL builds the release-download URL for an asset.
func RawDownloadURL(owner, repo, tag, asset string) string {
	return "https://github.com/" + EscapePath(owner) + "/" + EscapePath(repo) + "/releases/download/" + EscapePath(tag) + "/" + EscapePath(asset)
}
