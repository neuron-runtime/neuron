package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/internal/githubrepo"
	"github.com/neuron-runtime/neuron/application/provider"
	providergithub "github.com/neuron-runtime/neuron/application/provider/source/github"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// releaseFixture describes one served release: its tag and asset bodies.
type releaseFixture struct {
	Tag    string
	Assets map[string][]byte
}

// newTestServer serves the GitHub endpoints the provider source touches.
// Download URLs are built from the request host so release JSON always
// points back at the live test server.
func newTestServer(t *testing.T, releases []releaseFixture) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	releaseJSON := func(base string, tag string) (map[string]any, bool) {
		for _, rel := range releases {
			if rel.Tag != tag {
				continue
			}
			assets := make([]map[string]any, 0, len(rel.Assets))
			for name := range rel.Assets {
				assets = append(assets, map[string]any{
					"name":                 name,
					"browser_download_url": base + "/assets/" + name,
				})
			}
			return map[string]any{
				"tag_name":   rel.Tag,
				"prerelease": false,
				"draft":      false,
				"assets":     assets,
			}, true
		}
		return nil, false
	}

	mux.HandleFunc("/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		list := make([]map[string]any, 0, len(releases))
		for _, rel := range releases {
			entry, _ := releaseJSON(base, rel.Tag)
			list = append(list, entry)
		}
		json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("/repos/o/r/releases/tags/", func(w http.ResponseWriter, r *http.Request) {
		tag := strings.TrimPrefix(r.URL.Path, "/repos/o/r/releases/tags/")
		entry, ok := releaseJSON("http://"+r.Host, tag)
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(entry)
	})

	for _, rel := range releases {
		for name, body := range rel.Assets {
			body := body
			mux.HandleFunc("/assets/"+name, func(w http.ResponseWriter, r *http.Request) {
				w.Write(body)
			})
		}
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newSource(t *testing.T, srv *httptest.Server) *providergithub.Registry {
	t.Helper()
	client := githubrepo.NewClient()
	client.BaseURL = srv.URL
	return providergithub.New(
		providergithub.WithClient(client),
		providergithub.WithCatalog(map[string]providergithub.RepoRef{
			"process": {Owner: "o", Repo: "r"},
		}),
	)
}

func TestVersionsFromReleases(t *testing.T) {
	srv := newTestServer(t, []releaseFixture{{Tag: "v1.1.0"}, {Tag: "v1.0.0"}})
	src := newSource(t, srv)

	versions, err := src.Versions(context.Background(), "process")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(versions) != 2 || versions[0] != "1.1.0" || versions[1] != "1.0.0" {
		t.Fatalf("Versions = %v, want [1.1.0 1.0.0]", versions)
	}
}

func TestUncataloguedNameIsRejected(t *testing.T) {
	src := newSource(t, newTestServer(t, nil))

	_, err := src.Versions(context.Background(), "not-listed")
	if err == nil || !strings.Contains(err.Error(), "not listed in the github provider catalog") {
		t.Fatalf("Versions(uncatalogued) = %v, want catalog rejection", err)
	}
}

func TestPackageUsesCanonicalArchiveAsset(t *testing.T) {
	archive := providertypes.PackageArchiveName("process", "1.0.0")
	srv := newTestServer(t, []releaseFixture{
		{Tag: "v1.0.0", Assets: map[string][]byte{archive: []byte("archive-bytes")}},
	})
	src := newSource(t, srv)

	pkg, err := src.Package(context.Background(), "process", "1.0.0")
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if pkg.Artifact.URL != srv.URL+"/assets/"+archive {
		t.Fatalf("Artifact.URL = %q, want server archive URL", pkg.Artifact.URL)
	}
	if pkg.Registry != "github" {
		t.Fatalf("Registry = %q, want github", pkg.Registry)
	}
}

func TestPackageFallsBackToManifestAndPlatformPayloads(t *testing.T) {
	manifest := fmt.Sprintf(`{
		"apiVersion": %q,
		"kind": %q,
		"metadata": {"name": "process", "version": "1.0.0"},
		"provider": {"protocolVersion": %q},
		"runtimeKinds": [%q],
		"platforms": {"linux-amd64": {"artifact": "process-linux-amd64"}}
	}`, providertypes.APIVersion, providertypes.Kind, providertypes.ProtocolVersion, capabilityrt.RuntimeKindProcess)

	srv := newTestServer(t, []releaseFixture{
		{Tag: "v1.0.0", Assets: map[string][]byte{
			providertypes.ManifestFile: []byte(manifest),
			"process-linux-amd64":      []byte("payload"),
		}},
	})
	src := newSource(t, srv)

	pkg, err := src.Package(context.Background(), "process", "1.0.0")
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if pkg.Source == "" {
		t.Fatal("fallback package must materialize a local source directory")
	}
	defer os.RemoveAll(pkg.Source)
	data, err := os.ReadFile(filepath.Join(pkg.Source, "process-linux-amd64"))
	if err != nil {
		t.Fatalf("platform payload: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("payload = %q", data)
	}
	if _, err := os.Stat(filepath.Join(pkg.Source, providertypes.ManifestFile)); err != nil {
		t.Fatalf("manifest in source dir: %v", err)
	}
}

func TestMissingReleaseIsNotFound(t *testing.T) {
	src := newSource(t, newTestServer(t, nil))
	if _, err := src.Package(context.Background(), "process", "9.9.9"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Package(missing) = %v, want ErrNotFound", err)
	}
}
