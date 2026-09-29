package capabilityruntime

import (
	"fmt"
	"strings"

	"github.com/neuron-runtime/neuron/application/capabilityruntime"
)

func printList(installed []capabilityruntime.Installed) {
	for _, i := range installed {
		fmt.Printf("%s\t%s\t%s\t%s\n", i.Type, i.Version, i.Registry, i.Digest)
	}
}

func printInspect(i *capabilityruntime.Installed) {
	fmt.Printf("Name:         %s\n", i.Type)
	fmt.Printf("Version:      %s\n", i.Version)
	fmt.Printf("Registry:     %s\n", orDash(i.Registry))
	fmt.Printf("Digest:       %s\n", orDash(i.Digest))
	fmt.Printf("Runtime:      %s\n", i.Runtime.Type)
	fmt.Printf("Protocol:     %s\n", orDash(i.Runtime.Protocol))
	fmt.Printf("Entrypoint:   %s\n", i.Runtime.Entrypoint)
	fmt.Printf("Root:         %s\n", i.RootDir)
	fmt.Printf("Platform:     %s\n", orDash(i.Platform))

	if len(i.Capabilities) > 0 {
		fmt.Printf("Capabilities:\n  %s\n", strings.Join(i.Capabilities, "\n  "))
	}
	if len(i.Capabilities) > 0 {
		fmt.Printf("Capabilities:\n  %s\n", strings.Join(i.Capabilities, "\n  "))
	}
}

func orDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
