// Package apitoken defines the credential contract between the neuron CLI and
// the N.O.R.E. API.
//
// N.O.R.E.'s API can register assemblies, create instances, and execute
// capability runtimes, which makes it an authority to run code. A local Unix
// socket is reachable by every process on the machine, so the socket alone is a
// convenience rather than a boundary. The API therefore requires a bearer
// token, and both sides need to agree on three things:
//
//   - how a client presents the token,
//   - where the daemon publishes it for local clients, and
//   - how a client supplies it when it is not local (a TCP endpoint).
//
// The token file lives beside the socket so a local client can find it without
// configuration, and it is written with owner-only permissions because it is
// the credential itself.
package apitoken

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvToken lets a client present the API token out of band, which is how a
	// client authenticates against a remote TCP endpoint where the local token
	// file is not available.
	EnvToken = "NEURON_API_TOKEN"

	// EnvTokenFile overrides the token file path for clients and for the daemon.
	EnvTokenFile = "NEURON_API_TOKEN_FILE"

	// AuthorizationHeader carries the bearer credential on every request.
	AuthorizationHeader = "Authorization"

	// scheme is the authorization scheme for the credential.
	scheme = "Bearer"

	// fileSuffix is appended to the socket path to derive the token file path.
	fileSuffix = ".token"

	// generatedTokenBytes is the entropy of an auto-generated token. 32 bytes
	// is far beyond brute force and keeps the token short enough to pass in a
	// header or on a command line.
	generatedTokenBytes = 32
)

// TokenFilePath returns the token file that belongs to a Unix socket path. The
// token sits next to the socket it protects: a client given a socket path can
// always derive the credential it needs.
func TokenFilePath(socketPath string) string {
	if socketPath == "" {
		return ""
	}
	return filepath.Clean(socketPath) + fileSuffix
}

// Generate returns a new random API token.
func Generate() (string, error) {
	buf := make([]byte, generatedTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Read returns the token stored at path. The boolean reports whether a token
// was found; a missing file is the caller's decision to handle, not an error,
// because a client may legitimately run against a daemon that authenticates
// through another channel.
func Read(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", false
	}
	return token, true
}

// Write stores token at path with owner-only permissions. An existing file is
// replaced, but its permissions are re-applied so a file created with a looser
// mode cannot silently keep it.
func Write(path, token string) error {
	if path == "" {
		return fmt.Errorf("api token file path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return fmt.Errorf("write api token file %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure api token file %s: %w", path, err)
	}
	return nil
}

// Header renders the credential as an Authorization header value.
func Header(token string) string {
	if token == "" {
		return ""
	}
	return scheme + " " + token
}
