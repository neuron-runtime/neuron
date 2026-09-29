// Command stubborn is a test fixture that refuses to exit gracefully.
//
// It signals readiness using the same ready-file protocol as a real capability
// runtime, then ignores SIGTERM forever. The process runtime can therefore only
// reclaim it by force-killing the process, which is the path the shutdown
// regression test needs to exercise.
package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Signal readiness exactly as a real capability runtime does, so the test
	// can prove it is killing a live, correctly-started process.
	if ready := os.Getenv("NEURON_CAPABILITY_RUNTIME_READY"); ready != "" {
		_ = os.WriteFile(ready, nil, 0o600)
	}

	// Deliberately swallow SIGTERM: only SIGKILL can stop this process.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)

	for {
		select {
		case <-sig:
			// Ignored on purpose.
		case <-time.After(50 * time.Millisecond):
		}
	}
}
