package capabilityruntime

import (
	"runtime"
	"testing"

	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

func TestPlatformForRuntime(t *testing.T) {
	cases := []struct {
		runtimeType string
		want        string
	}{
		{capabilityrt.RuntimeKindWasm, capabilityrt.PlatformWasm},
		{capabilityrt.RuntimeKindProcess, HostPlatform()},
		{"", HostPlatform()},
		{capabilityrt.RuntimeKindContainer, HostPlatform()},
		{capabilityrt.RuntimeKindRemote, HostPlatform()},
	}
	for _, tc := range cases {
		if got := PlatformForRuntime(tc.runtimeType); got != tc.want {
			t.Errorf("PlatformForRuntime(%q) = %q, want %q", tc.runtimeType, got, tc.want)
		}
	}
	if HostPlatform() == capabilityrt.PlatformWasm {
		t.Error("HostPlatform must not collide with the wasm platform key")
	}
}

func TestHostPlatformShape(t *testing.T) {
	got := HostPlatform()
	if got != runtime.GOOS+"-"+runtime.GOARCH {
		t.Errorf("HostPlatform() = %q, want %q", got, runtime.GOOS+"-"+runtime.GOARCH)
	}
}
