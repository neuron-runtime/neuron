package capabilityruntime

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
)

// Downloader fetches package artifacts. Implementations must be safe for
// concurrent use.
type Downloader interface {
	// Download retrieves the resource at url into dst. When url addresses a
	// directory, dst is created as a directory and populated recursively.
	Download(ctx context.Context, url, dst string) error
}

// HTTPDownloader fetches artifacts over http(s):// and file:// URLs. It is the
// default downloader used by the Installer.
type HTTPDownloader struct {
	Client *http.Client
}

// NewHTTPDownloader returns a downloader with sane timeouts.
func NewHTTPDownloader() *HTTPDownloader {
	return &HTTPDownloader{
		Client: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (d *HTTPDownloader) Download(ctx context.Context, rawURL, dst string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse artifact url: %w", err)
	}

	switch u.Scheme {
	case "", "file":
		return copyLocal(u.Path, dst)
	case "http", "https":
		return d.downloadRemote(ctx, rawURL, dst)
	default:
		return fmt.Errorf("unsupported artifact url scheme %q", u.Scheme)
	}
}

func (d *HTTPDownloader) downloadRemote(ctx context.Context, rawURL, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}

	resp, err := d.Client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %d", rawURL, resp.StatusCode)
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("download %s: %w", rawURL, err)
	}
	return nil
}

func copyLocal(source, dst string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("artifact %s: %w", source, err)
	}

	if info.IsDir() {
		return copyDir(source, dst)
	}

	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(source, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := info.Mode()
		if mode&0o111 != 0 {
			mode = 0o755
		} else {
			mode = 0o644
		}
		return os.WriteFile(target, data, mode)
	})
}

// FileName returns the base file name of a URL path.
func FileName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	base := filepath.Base(u.Path)
	if base == "" || base == "." || strings.HasSuffix(base, "/") {
		return ""
	}
	return base
}
