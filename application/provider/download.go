package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/application/internal/artifact"
)

// Downloader fetches a package artifact into a local temporary file. It is
// the provider-side equivalent of the capability runtime downloader: sources
// describe *where* an artifact lives, the installer owns fetching and
// verifying it before anything becomes visible in the store.
type Downloader interface {
	// Download fetches url. When expectSHA256 is non-empty, the downloaded
	// file's digest must match it (artifact.ErrChecksumMismatch otherwise).
	// The returned path is temporary; the caller must remove it.
	Download(ctx context.Context, rawURL, expectSHA256 string) (path string, err error)
}

// HTTPDownloader fetches http(s) and file:// artifact URLs.
type HTTPDownloader struct {
	Client *http.Client
}

// NewHTTPDownloader returns a downloader with a bounded timeout.
func NewHTTPDownloader() *HTTPDownloader {
	return &HTTPDownloader{Client: &http.Client{Timeout: 5 * time.Minute}}
}

// Download fetches rawURL into a temporary file inside dir. file:// URLs are
// copied; http(s) URLs are downloaded. The SHA-256 digest of the saved bytes
// is verified when expectSHA256 is declared.
func (d *HTTPDownloader) Download(ctx context.Context, rawURL, expectSHA256 string) (string, error) {
	dir, err := os.MkdirTemp("", "neuron-provider-*")
	if err != nil {
		return "", err
	}

	data, err := d.fetch(ctx, rawURL)
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}

	name := downloadName(rawURL)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		os.RemoveAll(dir)
		return "", err
	}

	if expectSHA256 != "" {
		got, err := artifact.DigestFile(path)
		if err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		if got != expectSHA256 {
			os.RemoveAll(dir)
			return "", fmt.Errorf("%w: %s digest %s, want %s", artifact.ErrChecksumMismatch, name, got, expectSHA256)
		}
	}

	return path, nil
}

func (d *HTTPDownloader) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	if strings.HasPrefix(rawURL, "file://") {
		return os.ReadFile(strings.TrimPrefix(rawURL, "file://"))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s: %s", rawURL, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// downloadName derives a local file name from the URL path, falling back to
// a stable placeholder so identical URLs map to identical names.
func downloadName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" {
		return "artifact"
	}
	base := filepath.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return "artifact"
	}
	return base
}
