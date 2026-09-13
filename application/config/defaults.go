package config

// Defaults returns the built-in Neuron configuration.
//
// These values are compiled into Neuron and are the base of every effective
// configuration produced by Load. Lower layers (global, project, environment,
// CLI) override them.
func Defaults() Config {
	return Config{
		Version: 1,
		Lang:    "typescript",

		Runtime: RuntimeConfig{
			Execution: ExecutionConfig{
				Mode:    "wait",
				Timeout: "30m",
			},
			Workers: WorkerConfig{
				Min: 1,
				Max: 8,
			},
		},

		Daemon: DaemonConfig{
			Endpoint: "",
			Socket:   DefaultLocalSocket(),
			PIDFile:  DefaultPIDFile(),
		},

		Storage: StorageConfig{
			Provider:  "file",
			Directory: DefaultDataDir(),
		},

		// No registries are compiled in. A project that resolves external
		// executors must declare them; the default registry list is only the
		// fallback for Requirements that name no registry.
		Executors: ExecutorsConfig{
			StoreDir:          DefaultStoreDir(),
			DefaultRegistries: []string{"local"},
		},

		Inspector: InspectorConfig{
			Enabled: true,
			Address: "127.0.0.1:7433",
		},

		Dev: DevConfig{
			MaxWorkers: 1,
		},
	}
}
