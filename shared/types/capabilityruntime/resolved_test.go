package capabilityruntime

import (
	"path/filepath"
	"testing"
)

func TestEntrypointPath(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "store", "github", "read", "1.2.0")

	tests := []struct {
		name       string
		rootDir    string
		entrypoint string
		want       string
	}{
		{
			name:       "nested entrypoint uses native separators",
			rootDir:    root,
			entrypoint: "bin/capability-runtime",
			want:       filepath.Join(root, "bin", "capability-runtime"),
		},
		{
			name:       "empty root returns entrypoint unchanged",
			rootDir:    "",
			entrypoint: "capability-runtime.sh",
			want:       "capability-runtime.sh",
		},
		{
			name:       "empty entrypoint returns empty",
			rootDir:    root,
			entrypoint: "",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ResolvedCapabilityRuntime{
				RootDir: tt.rootDir,
				Runtime: RuntimeInfo{Entrypoint: tt.entrypoint},
			}
			if got := r.EntrypointPath(); got != tt.want {
				t.Errorf("EntrypointPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
