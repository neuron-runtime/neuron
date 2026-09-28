package process

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

// legacyInstance runs an capability runtime as a one-shot OS subprocess speaking the
// stdin/stdout JSON protocol. Each execution spawns a fresh process, feeds
// it one JSON Request on stdin, and reads one JSON Response from stdout.
//
// This is the transport for WASI capability runtimes and for process capability runtimes that
// predate the gRPC protocol (declared via neuron/capability-runtime-v1-json). It is
// intentionally simple: no worker reuse, no long-lived process. CapabilityRuntimes
// that need connection reuse MUST be migrated to the gRPC protocol.
type legacyInstance struct {
	type_      string
	version    string
	entrypoint string
	protocol   string
	timeout    time.Duration
	logger     *slog.Logger
}

// newLegacyInstance builds a one-shot JSON subprocess instance for a frozen
// capability runtime spec.
func newLegacyInstance(spec capabilityrt.BackendSpec, logger *slog.Logger) (capabilityrt.BackendInstance, error) {
	return &legacyInstance{
		type_:      spec.Type,
		version:    spec.Version,
		entrypoint: resolveEntrypoint(spec.RootDir, spec.Entrypoint),
		protocol:   spec.Protocol,
		timeout:    10 * time.Minute,
		logger:     logger.With("capability_runtime", spec.Type, "version", spec.Version),
	}, nil
}

// Execute spawns the process, feeds it the protocol request, and returns the
// parsed response output.
func (l *legacyInstance) Execute(ctx context.Context, req *capabilityrt.Request) (*capabilityrt.Response, error) {
	if req == nil {
		req = &capabilityrt.Request{}
	}

	timeout := l.timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, l.entrypoint)
	cmd.Env = append(os.Environ(),
		capabilityrt.EnvProtocol+"="+l.protocol,
		capabilityrt.EnvType+"="+l.type_,
		capabilityrt.EnvVersion+"="+l.version,
	)

	stdin, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("capability runtime %s: encode request: %w", l.type_, err)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("capability runtime %s: timed out after %s", l.type_, timeout)
		}
		return nil, fmt.Errorf("capability runtime %s: %w: %s", l.type_, err, stderr.String())
	}

	var resp capabilityrt.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("capability runtime %s: decode response: %w (stderr: %s)", l.type_, err, strings.TrimSpace(stderr.String()))
	}

	return &resp, nil
}

// Health reports the entrypoint is present and executable. There is no
// long-lived process to probe; the next Execute starts a fresh one.
func (l *legacyInstance) Health(ctx context.Context) error {
	info, err := os.Stat(l.entrypoint)
	if err != nil {
		return fmt.Errorf("capability runtime %s: %w", l.type_, err)
	}
	if info.IsDir() {
		return fmt.Errorf("capability runtime %s: entrypoint is a directory", l.type_)
	}
	return nil
}

// Close is a no-op: one-shot executions own their process lifecycle.
func (l *legacyInstance) Close(ctx context.Context) error {
	return nil
}

// SetTimeout overrides the per-execution timeout (for tests).
func (l *legacyInstance) SetTimeout(d time.Duration) {
	l.timeout = d
}
