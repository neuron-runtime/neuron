package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/neuron-runtime/neuron/application/provider"
	providerstore "github.com/neuron-runtime/neuron/application/provider/store"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
	"github.com/spf13/cobra"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "provider",
		Short:  "Provider artifact management (advanced)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newBuildCmd())
	return cmd
}

func newBuildCmd() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:    "build",
		Short:  "Build first-party provider artifacts into the store layout",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := provider.DefaultStoreDir()
			if outDir != "" {
				root = outDir
			}
			store, err := providerstore.NewFileSystemStore(root)
			if err != nil {
				return err
			}
			installer := &provider.Installer{Store: store}
			if err := buildAndInstall(ctx, installer, "process", "0.1.0", capabilityrt.RuntimeKindProcess); err != nil {
				return err
			}
			if err := buildAndInstall(ctx, installer, "wasm", "0.1.0", capabilityrt.RuntimeKindWasm); err != nil {
				return err
			}
			fmt.Printf("built provider artifacts in %s\n", store.Root())
			return nil
		},
	}
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "output store root (defaults to ~/.neuron/providers)")
	return cmd
}

func buildAndInstall(ctx context.Context, installer *provider.Installer, name, version, kind string) error {
	tmp := os.TempDir()
	staged, err := os.MkdirTemp(tmp, "provider-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)

	host := runtime.GOOS + "-" + runtime.GOARCH
	if kind == capabilityrt.RuntimeKindWasm {
		host = capabilityrt.PlatformWasm
	}

	m := &providertypes.Manifest{
		APIVersion:   providertypes.APIVersion,
		Kind:         providertypes.Kind,
		Metadata:     providertypes.ManifestMetadata{Name: name, Version: version},
		RuntimeKinds: []string{kind},
		Protocols:    []string{capabilityrt.ProtocolV1, capabilityrt.ProtocolJSONV1},
		Platforms:    map[string]providertypes.Platform{host: {Artifact: "provider"}},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staged, "provider.runtime.json"), data, 0o644); err != nil {
		return err
	}
	pkg := &provider.Package{Name: name, Version: version, Manifest: data, Source: staged}
	res, err := installer.Install(ctx, pkg)
	if err != nil {
		return err
	}
	if res.AlreadyPresent {
		fmt.Printf("provider %s@%s already present\n", name, version)
	} else {
		fmt.Printf("installed provider %s@%s\n", name, version)
	}
	return nil
}
