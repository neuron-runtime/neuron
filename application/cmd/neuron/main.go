package main

import (
	"fmt"
	"os"

	"github.com/Muhammad-Jay/neuron/application/internal/cli"
)

func main() {
	// The root command never prints errors itself (cobra's SilenceErrors and
	// SilenceUsage are set in cli.go); this is the single, uniform error
	// surface for every command.
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "\u2717 %s\n", err)
		os.Exit(1)
	}
}
