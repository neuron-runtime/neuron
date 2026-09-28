package github

import (
	"context"
	"sort"
)

// Release is the subset of the GitHub Releases API record the registry needs.
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

// ListReleases fetches published releases for a repository.
func (c *Client) ListReleases(ctx context.Context, owner, repo string) ([]Release, error) {
	endpoint := "repos/" + escapePath(owner) + "/" + escapePath(repo) + "/releases?per_page=100"

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
	endpoint := "repos/" + escapePath(owner) + "/" + escapePath(repo) + "/releases/tags/" + escapePath(tag)

	var release Release
	if err := c.do(ctx, "GET", endpoint, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// versionsFromReleases extracts semver-ish versions from release tags, newest
// first. Only non-empty, valid version strings survive SelectVersion upstream,
// but prereleases are excluded here unless no stable release exists.
func versionsFromReleases(releases []Release) []string {
	var stable []string
	var prerelease []string
	for _, r := range releases {
		v := parseTagToVersion(r.TagName)
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

// assetByName finds a release asset by exact name.
func (r *Release) assetByName(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}
