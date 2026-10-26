package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// DefaultDriver is selected when Config.Driver is empty. It keeps the simple
// deployment path working without making runtime code choose a backend.
const DefaultDriver = "sqlite"

// DriverFunc constructs a ready-to-use backend. It must open the underlying
// resource and initialize its schema; Open performs the health check and closes
// the backend if construction or the health check fails.
type DriverFunc func(ctx context.Context, cfg Config) (Store, error)

var (
	driversMu sync.RWMutex
	drivers   = make(map[string]DriverFunc)
)

// RegisterDriver makes a backend selectable by name. Backends register
// themselves from init so only the composition root needs to import them.
// Registering an empty or duplicate name is a programmer error.
func RegisterDriver(name string, fn DriverFunc) {
	name = normalizeDriver(name)
	if name == "" {
		panic("storage: driver name is required")
	}
	if fn == nil {
		panic("storage: driver constructor is required")
	}

	driversMu.Lock()
	defer driversMu.Unlock()
	if _, exists := drivers[name]; exists {
		panic("storage: duplicate driver " + name)
	}
	drivers[name] = fn
}

// Open constructs the configured backend, verifies it responds to Ping, and
// returns it. An unknown driver is an actionable error rather than a silent
// fallback. Open is the only construction path used by the runtime.
func Open(ctx context.Context, cfg Config) (Store, error) {
	name := normalizeDriver(cfg.Driver)
	if name == "" {
		name = DefaultDriver
	}
	cfg.Driver = name

	driversMu.RLock()
	driver, ok := drivers[name]
	driversMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q (available: %s)", ErrUnknownDriver, name, availableDrivers())
	}

	store, err := driver(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open storage driver %q: %w", name, err)
	}
	if store == nil {
		return nil, fmt.Errorf("open storage driver %q: constructor returned nil", name)
	}

	if err := store.Ping(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("ping storage driver %q: %w", name, err)
	}

	return store, nil
}

func normalizeDriver(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func availableDrivers() string {
	driversMu.RLock()
	defer driversMu.RUnlock()

	names := make([]string, 0, len(drivers))
	for name := range drivers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
