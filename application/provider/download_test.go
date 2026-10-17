package provider_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/neuron-runtime/neuron/application/internal/artifact"
	"github.com/neuron-runtime/neuron/application/provider"
)

// writeTarGz packs dir's files (paths relative to dir) into a .tar.gz.
func writeTarGz(t *testing.T, dir, archive string) {
	t.Helper()
	f, err := os.Create(archive)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name: filepath.ToSlash(rel),
			Mode: 0o644,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		t.Fatalf("walk source dir: %v", err)
	}
}

func TestHTTPDownloaderFileURL(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "pkg.tar.gz")
	if err := os.WriteFile(payload, []byte("payload-bytes"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	d := provider.NewHTTPDownloader()
	got, err := d.Download(context.Background(), "file://"+payload, "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(got))
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read downloaded: %v", err)
	}
	if string(data) != "payload-bytes" {
		t.Fatalf("downloaded = %q", data)
	}
}

func TestHTTPDownloaderVerifiesSHA256(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "pkg.bin")
	if err := os.WriteFile(payload, []byte("payload-bytes"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	sum, err := artifact.DigestFile(payload)
	if err != nil {
		t.Fatalf("digest payload: %v", err)
	}

	d := provider.NewHTTPDownloader()
	if _, err := d.Download(context.Background(), "file://"+payload, sum); err != nil {
		t.Fatalf("Download with correct digest: %v", err)
	}
	_, err = d.Download(context.Background(), "file://"+payload, "sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if !errors.Is(err, artifact.ErrChecksumMismatch) {
		t.Fatalf("Download with wrong digest = %v, want ErrChecksumMismatch", err)
	}
}

func TestInstallFromArtifactFileURL(t *testing.T) {
	// Build a real archive the installer can extract.
	srcDir := writeSource(t, "process", "1.0.0", map[string]string{"payload.bin": "hello"})
	archive := filepath.Join(t.TempDir(), "process-1.0.0-provider.neuron.tar.gz")
	writeTarGz(t, srcDir, archive)

	installer, _ := newInstaller(t)
	res, err := installer.Install(context.Background(), &provider.Package{
		Name:     "process",
		Version:  "1.0.0",
		Registry: "local",
		Artifact: provider.Artifact{URL: "file://" + archive},
	})
	if err != nil {
		t.Fatalf("Install from artifact URL: %v", err)
	}
	if res.Installed.Version != "1.0.0" {
		t.Fatalf("installed = %+v", res.Installed)
	}
}
