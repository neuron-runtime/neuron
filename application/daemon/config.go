package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/neuron-runtime/neuron/application/config"
)

var (
	ErrAlreadyRunning = errors.New("nore daemon is already running")
	ErrNotRunning     = errors.New("nore daemon is not running")
	ErrStartTimeout   = errors.New("timed out waiting for nore daemon to become healthy")
	ErrInvalidConfig  = errors.New("invalid daemon configuration")
)

type Config struct {
	BinaryPath     string
	Args           []string
	WorkingDir     string
	Env            []string
	StartTimeout   time.Duration
	HealthInterval time.Duration
	AttachOutput   bool
	PIDFile        string
}

func DefaultConfig() Config {
	return Config{
		BinaryPath:     "nore-daemon",
		StartTimeout:   10 * time.Second,
		HealthInterval: 100 * time.Millisecond,
		AttachOutput:   true,
		PIDFile:        filepath.Join(os.TempDir(), "nore.pid"),
	}
}

// ConfigFromEffective builds a daemon Config from the effective Neuron
// configuration. It translates the config's daemon/storage/runtime settings
// into the `nore` flags the daemon process accepts.
//
// Cross-module note: `nore` is a separate binary, so the effective config must
// reach it through process flags rather than shared types.
func ConfigFromEffective(cfg config.Config) Config {
	c := DefaultConfig()

	if cfg.Daemon.NorePath != "" {
		c.BinaryPath = cfg.Daemon.NorePath
	}

	if cfg.Daemon.Socket != "" {
		c.Args = append(c.Args, "--socket", cfg.Daemon.Socket)
	}

	if cfg.Storage.Directory != "" {
		c.Args = append(c.Args, "--data-dir", cfg.Storage.Directory)
	}

	if cfg.Runtime.Workers.Max > 0 {
		c.Args = append(c.Args, "--workers", strconv.Itoa(cfg.Runtime.Workers.Max))
	}

	if cfg.Runtime.DetachedDrainTimeout != "" {
		c.Args = append(c.Args, "--detached-drain-timeout", cfg.Runtime.DetachedDrainTimeout)
	}

	if cfg.Daemon.PIDFile != "" {
		c.PIDFile = cfg.Daemon.PIDFile
	}

	return c
}
