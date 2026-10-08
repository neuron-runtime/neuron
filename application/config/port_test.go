package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCoerceRuntimePort(t *testing.T) {
	auto := RuntimePort{Mode: RuntimePortAuto}
	fixed := func(n int) RuntimePort { return RuntimePort{Mode: RuntimePortFixed, Number: n} }

	tests := []struct {
		name    string
		raw     any
		want    RuntimePort
		wantErr string
	}{
		{name: "normalized value passes through", raw: fixed(9000), want: fixed(9000)},
		{name: "nil is auto", raw: nil, want: auto},
		{name: "auto string", raw: "auto", want: auto},
		{name: "auto uppercase", raw: "AUTO", want: auto},
		{name: "auto padded", raw: " Auto ", want: auto},
		{name: "numeric string", raw: "7000", want: fixed(7000)},
		{name: "int", raw: 6000, want: fixed(6000)},
		{name: "int64", raw: int64(6001), want: fixed(6001)},
		{name: "integral float", raw: float64(6002), want: fixed(6002)},
		{name: "lowest valid port", raw: 1, want: fixed(1)},
		{name: "highest valid port", raw: 65535, want: fixed(65535)},
		{name: "zero", raw: 0, wantErr: "runtime.port must be"},
		{name: "above range", raw: 70000, wantErr: "runtime.port must be"},
		{name: "negative", raw: -1, wantErr: "runtime.port must be"},
		{name: "non-numeric string", raw: "abc", wantErr: "runtime.port must be"},
		{name: "numeric string out of range", raw: "70000", wantErr: "runtime.port must be"},
		{name: "bool", raw: true, wantErr: "runtime.port must be"},
		{name: "fractional float", raw: 60.5, wantErr: "runtime.port must be"},
		{name: "unsupported type", raw: []string{"auto"}, wantErr: "runtime.port must be"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := coerceRuntimePort(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("coerceRuntimePort(%#v) = %+v, want error", tt.raw, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("coerceRuntimePort(%#v) failed: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("coerceRuntimePort(%#v) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestLoadRuntimePortCoercion(t *testing.T) {
	auto := RuntimePort{Mode: RuntimePortAuto}
	fixed := func(n int) RuntimePort { return RuntimePort{Mode: RuntimePortFixed, Number: n} }

	tests := []struct {
		name    string
		port    string // YAML fragment; empty omits the key
		want    RuntimePort
		wantErr string
	}{
		{name: "integer", port: "6000", want: fixed(6000)},
		{name: "auto string", port: `"auto"`, want: auto},
		{name: "auto uppercase", port: `"AUTO"`, want: auto},
		{name: "numeric string", port: `"7000"`, want: fixed(7000)},
		{name: "absent defaults to auto", port: "", want: auto},
		{name: "zero", port: "0", wantErr: "runtime.port must be"},
		{name: "above range", port: "70000", wantErr: "runtime.port must be"},
		{name: "non-numeric string", port: `"abc"`, wantErr: "runtime.port must be"},
		{name: "bool", port: "true", wantErr: "runtime.port must be"},
		{name: "fractional float", port: "60.5", wantErr: "runtime.port must be"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := "runtime:\n"
			if tt.port != "" {
				content += "  port: " + tt.port + "\n"
			}
			cfg, err := loadProjectConfig(t, "neuron.config.yaml", content)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Load succeeded with port %s, want error", tt.port)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}
			if cfg.Runtime.Port != tt.want {
				t.Errorf("runtime.port = %+v, want %+v", cfg.Runtime.Port, tt.want)
			}
		})
	}
}

func TestRuntimePortDefaultIsAuto(t *testing.T) {
	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Runtime.Port.IsAuto() {
		t.Errorf("default runtime.port = %+v, want auto", cfg.Runtime.Port)
	}
}

func TestRuntimePortEnvOverride(t *testing.T) {
	t.Setenv("NEURON_RUNTIME_PORT", "6001")

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectDir:  t.TempDir(),
		Environment: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := RuntimePort{Mode: RuntimePortFixed, Number: 6001}
	if cfg.Runtime.Port != want {
		t.Errorf("runtime.port = %+v, want %+v (environment wins)", cfg.Runtime.Port, want)
	}
}

func TestRuntimePortCLIWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.yaml", "runtime:\n  port: auto\n")

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
		CLI:         map[string]any{"runtime.port": 8002},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := RuntimePort{Mode: RuntimePortFixed, Number: 8002}
	if cfg.Runtime.Port != want {
		t.Errorf("runtime.port = %+v, want %+v (CLI wins over file)", cfg.Runtime.Port, want)
	}
}
