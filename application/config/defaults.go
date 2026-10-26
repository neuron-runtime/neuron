package config

import "github.com/neuron-runtime/neuron/shared/types/builtins"

// Defaults returns the built-in Neuron configuration.
//
// These values are compiled into Neuron and are the base of every effective
// configuration produced by Load. Lower layers (global, project, environment,
// CLI) override them.
func Defaults() Config {
	// The built-in limits default to the ceilings compiled into the runtime,
	// so the effective configuration states the limits that actually apply.
	// A project may lower them; it can never raise them.
	ceilings := builtins.DefaultPolicy()

	return Config{
		Lang: "typescript",

		Runtime: RuntimeConfig{
			Host: "localhost",
			Port: RuntimePort{Mode: RuntimePortAuto},
			Execution: ExecutionConfig{
				Mode:    "wait",
				Timeout: "30m",
			},
			Workers: WorkerConfig{
				Min: 1,
				Max: 8,
			},
			DetachedDrainTimeout: "30s",
		},

		Daemon: DaemonConfig{
			Endpoint: "",
			Socket:   DefaultLocalSocket(),
			PIDFile:  DefaultPIDFile(),
		},

		Storage: StorageConfig{
			Provider:  "sqlite",
			Directory: DefaultDataDir(),
		},

		// No registries are compiled in. A project that resolves external
		// capability runtimes must declare them; the default registry list is only the
		// fallback for Requirements that name no registry.
		CapabilityRuntimes: CapabilityRuntimesConfig{
			StoreDir:          DefaultStoreDir(),
			DefaultRegistries: []string{"local"},
		},

		// Like capability runtimes, no provider registries are compiled in;
		// the default registry list is the fallback for requirements that
		// name none.
		Providers: ProvidersConfig{
			StoreDir:          DefaultProviderStoreDir(),
			DefaultRegistries: []string{"local"},
		},

		Inspector: InspectorConfig{
			Enabled: true,
			Address: "127.0.0.1:7433",
		},

		Dev: DevConfig{
			MaxWorkers: 1,
		},

		Builtins: BuiltinsConfig{
			Limits: BuiltinLimitsConfig{
				MaxCommandOutputBytes:   ceilings.MaxCommandOutputBytes,
				MaxHTTPRequestBodyBytes: ceilings.MaxHTTPRequestBodyBytes,
				MaxHTTPResponseBytes:    ceilings.MaxHTTPResponseBytes,
				MaxFileReadBytes:        ceilings.MaxFileReadBytes,
				MaxDirectoryEntries:     ceilings.MaxDirectoryEntries,
				DefaultTimeout:          ceilings.DefaultTimeout,
			},
		},
	}
}
