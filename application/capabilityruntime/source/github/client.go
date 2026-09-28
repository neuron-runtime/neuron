package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// gitHubAPI is the default REST API base.
const gitHubAPI = "https://api.github.com"

// notFoundSentinel lets the registry translate API 404s into
// capabilityruntime.ErrNotFound so the resolver can try the next registry.
var notFoundSentinel = errors.New("github: not found")

// Client is a minimal, unauthenticated-or-token GitHub REST/raw client. It
// intentionally avoids SDKs: the registry only needs releases, assets, and raw
// file contents.
type Client struct {
	HTTP *http.Client

	// Token, when non-empty, enables higher rate limits and private
	// repositories (personal access token).
	Token string

	// BaseURL overrides the API base (default https://api.github.com).
	BaseURL string
}

func NewClient() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		BaseURL: gitHubAPI,
	}
}

// GitHubError carries the API message for a non-2xx response.
type GitHubError struct {
	StatusCode int
	Endpoint   string
	Message    string
}

func (e *GitHubError) Error() string {
	return fmt.Sprintf("github api %s: %d %s", e.Endpoint, e.StatusCode, e.Message)
}

func (c *Client) do(ctx context.Context, method, endpoint string, target any) error {
	req, err := c.newRequest(ctx, method, endpoint)
	if err != nil {
		return err
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github request %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if err := c.checkStatus(endpoint, resp); err != nil {
		return err
	}

	if target == nil {
		return nil
	}

	dec := json.NewDecoder(resp.Body)
	return dec.Decode(target)
}

// Get returns raw bytes for a URL (raw.githubusercontent.com or an asset URL).
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return io.ReadAll(resp.Body)
	case resp.StatusCode == http.StatusNotFound:
		return nil, errNotFound(rawURL)
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download %s: %d %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (c *Client) newRequest(ctx context.Context, method, endpoint string) (*http.Request, error) {
	base := strings.TrimSuffix(c.BaseURL, "/")
	if base == "" {
		base = gitHubAPI
	}

	reqURL := endpoint
	if !strings.HasPrefix(endpoint, "http") {
		reqURL = base + "/" + strings.TrimPrefix(endpoint, "/")
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	c.authorize(req)
	return req, nil
}

func (c *Client) authorize(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c *Client) checkStatus(endpoint string, resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body)
	message := body.Message
	if message == "" {
		message = resp.Status
	}

	if resp.StatusCode == http.StatusNotFound {
		return errNotFound(endpoint)
	}

	return &GitHubError{
		StatusCode: resp.StatusCode,
		Endpoint:   endpoint,
		Message:    message,
	}
}

// releaseNotFound marks a 404 so the resolver can try the next registry.
func errNotFound(endpoint string) error {
	return fmt.Errorf("%w: github %s", notFoundSentinel, endpoint)
}

// parseTagToVersion strips a leading "v": "v1.2.0" -> "1.2.0".
func parseTagToVersion(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

// escapePath encodes a path segment for API URLs.
func escapePath(path string) string {
	return url.PathEscape(path)
}
