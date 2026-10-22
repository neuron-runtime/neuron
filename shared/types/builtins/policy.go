// Package builtins defines the security policy that bounds the built-in
// capability runtimes N.O.R.E. provides.
//
// The built-ins reach outside the runtime: they open sockets, run programs,
// and touch the filesystem. A capability runtime that can do that without
// limits is a capability runtime whose limits are whatever the author of the
// assembly decides, so the limits live here, in one place, and are enforced
// by the runtime rather than by convention.
//
// The policy is split in two.
//
// The ceilings are compiled into the runtime. They are the strongest limits
// that exist, and no configuration can raise them: a project configuration
// file is part of the assembly's own input, and an input must not be able to
// widen the blast radius of a capability.
//
// The policy is what a project configures on top of those ceilings. It can
// only narrow them. Effective resolves the two into the limits the runtime
// actually applies, and refuses a configuration that asks for more than the
// ceilings allow.
package builtins

import (
	"fmt"
	"strings"
	"time"
)

// Policy bounds what the built-in capability runtimes are allowed to do.
//
// A zero field means "not configured", which is why the numeric limits have
// no meaningful zero value: a limit of zero bytes would disable a built-in
// rather than restrict it, and a configuration that wants that should say so
// by not using the built-in.
type Policy struct {
	// FilesystemRoots are the roots the filesystem and command built-ins may
	// reach in addition to the project root.
	//
	// The project root is always permitted: it is where the assembly lives.
	// These roots extend that reach to directories the assembly is expected
	// to work with. A path that resolves outside every permitted root is
	// refused.
	FilesystemRoots []string `json:"filesystemRoots,omitempty"`

	// AllowedExecutables restricts the programs the command built-in may run.
	//
	// When it is empty, any program resolvable on the program path may run.
	// When it is not empty, only the listed programs may run. A listed
	// program is matched by its resolved absolute path, so the match does not
	// depend on the working directory or on the order of the program path.
	AllowedExecutables []string `json:"allowedExecutables,omitempty"`

	// AllowedHTTPHosts restricts the http built-in to a set of hosts.
	//
	// When it is empty, the built-in may contact any public host and refuses
	// loopback, private, link-local, multicast, unspecified, and cloud
	// metadata addresses.
	//
	// When it is not empty, the built-in may contact only the listed hosts. A
	// listed host is permitted even when it resolves to a private address,
	// because listing it is the explicit and auditable decision to let a
	// capability reach an internal service.
	AllowedHTTPHosts []string `json:"allowedHTTPHosts,omitempty"`

	// MaxCommandOutputBytes bounds the output retained from one stream of a
	// single command.
	MaxCommandOutputBytes int64 `json:"maxCommandOutputBytes,omitempty"`

	// MaxHTTPRequestBodyBytes bounds the request body a single http request
	// may send.
	MaxHTTPRequestBodyBytes int64 `json:"maxHTTPRequestBodyBytes,omitempty"`

	// MaxHTTPResponseBytes bounds the response body a single http request
	// may read.
	MaxHTTPResponseBytes int64 `json:"maxHTTPResponseBytes,omitempty"`

	// MaxFileReadBytes bounds the file content a single filesystem read may
	// return.
	MaxFileReadBytes int64 `json:"maxFileReadBytes,omitempty"`

	// MaxDirectoryEntries bounds the entries a single filesystem listing may
	// return.
	MaxDirectoryEntries int `json:"maxDirectoryEntries,omitempty"`

	// DefaultTimeout bounds a single built-in operation when the capability
	// does not set a timeout of its own.
	//
	// It is written as a duration string such as "60s" so that the value
	// survives the JSON round trip between the compiler, the manifest, and
	// the runtime without losing its meaning.
	DefaultTimeout string `json:"defaultTimeout,omitempty"`
}

// DefaultPolicy returns the compiled-in ceilings for the built-in capability
// runtimes.
//
// These are the strongest limits that exist. A project may lower them
// through configuration but can never raise them.
func DefaultPolicy() Policy {
	return Policy{
		MaxCommandOutputBytes:   1 << 20, // 1 MiB
		MaxHTTPRequestBodyBytes: 1 << 20, // 1 MiB
		MaxHTTPResponseBytes:    8 << 20, // 8 MiB
		MaxFileReadBytes:        8 << 20, // 8 MiB
		MaxDirectoryEntries:     1000,
		DefaultTimeout:          "60s",
	}
}

// Effective resolves the limits the runtime applies from the compiled-in
// ceilings and the policy a project configured.
//
// A configured limit replaces the ceiling when it is lower, and the
// resolution fails when it is higher: the ceiling exists precisely so that a
// project's own configuration cannot widen what a capability can reach.
//
// The resolution is idempotent. Applying it to an already effective policy
// returns the same policy, which lets the runtime re-apply it to a policy it
// received over a transport without the transport being trusted.
func Effective(configured Policy) (Policy, error) {
	ceilings := DefaultPolicy()
	effective := ceilings

	limits := []struct {
		name       string
		configured int64
		ceiling    int64
		apply      func(*Policy, int64)
	}{
		{"maxCommandOutputBytes", configured.MaxCommandOutputBytes, ceilings.MaxCommandOutputBytes, func(p *Policy, v int64) {
			p.MaxCommandOutputBytes = v
		}},
		{"maxHTTPRequestBodyBytes", configured.MaxHTTPRequestBodyBytes, ceilings.MaxHTTPRequestBodyBytes, func(p *Policy, v int64) {
			p.MaxHTTPRequestBodyBytes = v
		}},
		{"maxHTTPResponseBytes", configured.MaxHTTPResponseBytes, ceilings.MaxHTTPResponseBytes, func(p *Policy, v int64) {
			p.MaxHTTPResponseBytes = v
		}},
		{"maxFileReadBytes", configured.MaxFileReadBytes, ceilings.MaxFileReadBytes, func(p *Policy, v int64) {
			p.MaxFileReadBytes = v
		}},
	}

	for _, limit := range limits {
		if limit.configured == 0 {
			continue
		}
		if limit.configured < 0 {
			return Policy{}, fmt.Errorf("built-in policy: %s must not be negative, got %d", limit.name, limit.configured)
		}
		if limit.configured > limit.ceiling {
			return Policy{}, fmt.Errorf("built-in policy: %s is %d, which exceeds the built-in ceiling of %d", limit.name, limit.configured, limit.ceiling)
		}
		limit.apply(&effective, limit.configured)
	}

	if configured.MaxDirectoryEntries != 0 {
		if configured.MaxDirectoryEntries < 0 {
			return Policy{}, fmt.Errorf("built-in policy: maxDirectoryEntries must not be negative, got %d", configured.MaxDirectoryEntries)
		}
		if configured.MaxDirectoryEntries > ceilings.MaxDirectoryEntries {
			return Policy{}, fmt.Errorf("built-in policy: maxDirectoryEntries is %d, which exceeds the built-in ceiling of %d",
				configured.MaxDirectoryEntries, ceilings.MaxDirectoryEntries)
		}
		effective.MaxDirectoryEntries = configured.MaxDirectoryEntries
	}

	if timeout := strings.TrimSpace(configured.DefaultTimeout); timeout != "" {
		ceiling, err := parseDuration(ceilings.DefaultTimeout)
		if err != nil {
			return Policy{}, fmt.Errorf("built-in policy: the built-in ceiling for defaultTimeout is invalid: %w", err)
		}
		requested, err := parseDuration(timeout)
		if err != nil {
			return Policy{}, fmt.Errorf("built-in policy: defaultTimeout must be a duration string such as \"60s\": %w", err)
		}
		if requested <= 0 {
			return Policy{}, fmt.Errorf("built-in policy: defaultTimeout must be positive, got %q", timeout)
		}
		if requested > ceiling {
			return Policy{}, fmt.Errorf("built-in policy: defaultTimeout is %s, which exceeds the built-in ceiling of %s", timeout, ceilings.DefaultTimeout)
		}
		effective.DefaultTimeout = timeout
	}

	effective.FilesystemRoots = normalizeRoots(configured.FilesystemRoots)
	effective.AllowedExecutables = normalizeList(configured.AllowedExecutables)
	effective.AllowedHTTPHosts = normalizeList(configured.AllowedHTTPHosts)

	return effective, nil
}

// Timeout returns the policy's default timeout as a duration.
func (p Policy) Timeout() (time.Duration, error) {
	timeout := strings.TrimSpace(p.DefaultTimeout)
	if timeout == "" {
		timeout = DefaultPolicy().DefaultTimeout
	}
	return parseDuration(timeout)
}

// Validate reports whether the policy is internally consistent.
//
// It is applied to a policy that has already been resolved through Effective
// when the runtime receives one, so it checks the values themselves rather
// than comparing them with the ceilings.
func (p Policy) Validate() error {
	if p.MaxCommandOutputBytes <= 0 {
		return fmt.Errorf("built-in policy: maxCommandOutputBytes must be positive, got %d", p.MaxCommandOutputBytes)
	}
	if p.MaxHTTPRequestBodyBytes <= 0 {
		return fmt.Errorf("built-in policy: maxHTTPRequestBodyBytes must be positive, got %d", p.MaxHTTPRequestBodyBytes)
	}
	if p.MaxHTTPResponseBytes <= 0 {
		return fmt.Errorf("built-in policy: maxHTTPResponseBytes must be positive, got %d", p.MaxHTTPResponseBytes)
	}
	if p.MaxFileReadBytes <= 0 {
		return fmt.Errorf("built-in policy: maxFileReadBytes must be positive, got %d", p.MaxFileReadBytes)
	}
	if p.MaxDirectoryEntries <= 0 {
		return fmt.Errorf("built-in policy: maxDirectoryEntries must be positive, got %d", p.MaxDirectoryEntries)
	}
	if _, err := p.Timeout(); err != nil {
		return fmt.Errorf("built-in policy: %w", err)
	}
	return nil
}

// normalizeRoots cleans the configured filesystem roots.
//
// A root is kept as written apart from surrounding whitespace, because a root
// is resolved against the filesystem rather than used as an identifier: the
// runtime resolves it to an absolute path before comparing it against a
// target path.
func normalizeRoots(roots []string) []string {
	if len(roots) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(roots))
	for _, root := range roots {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// normalizeList cleans a configured list of names.
func normalizeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// parseDuration parses a duration string.
func parseDuration(value string) (time.Duration, error) {
	return time.ParseDuration(strings.TrimSpace(value))
}
