package installcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/neuron-runtime/neuron/application/internal/artifact"
	"github.com/neuron-runtime/neuron/application/provider"
	providerstore "github.com/neuron-runtime/neuron/application/provider/store"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
	"github.com/spf13/cobra"
)

func New() *cobra.Command {
	var providerFlag bool
	var storeRoot string
	cmd := &cobra.Command{
		Use:    "install [ref]",
		Short:  "Install a provider artifact (advanced)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			if providerFlag {
				return installProvider(cmd.Context(), ref, storeRoot)
			}
			return fmt.Errorf("unsupported install target; use --provider for provider artifacts")
		},
	}
	cmd.Flags().BoolVar(&providerFlag, "provider", false, "install a provider artifact")
	cmd.Flags().StringVar(&storeRoot, "store-root", "", "override provider store root")
	return cmd
}

func installProvider(ctx context.Context, ref, storeRoot string) error {
	root := provider.DefaultStoreDir()
	if storeRoot != "" {
		root = storeRoot
	}
	store, err := providerstore.NewFileSystemStore(root)
	if err != nil {
		return err
	}
	installer := &provider.Installer{Store: store}

	info, err := os.Stat(ref)
	if err != nil {
		return err
	}

	tmp := os.TempDir()
	staged, err := os.MkdirTemp(tmp, "install-probe-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)

	if info.IsDir() {
		if err := copyDir(ref, staged); err != nil {
			return err
		}
	} else if artifact.IsArchive(ref) {
		if err := artifact.ExtractTarGz(ref, staged); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("unsupported provider source")
	}

	m, err := provider.ReadManifest(filepath.Join(staged, providertypes.ManifestFile))
	if err != nil {
		return err
	}
	pkg := &provider.Package{
		Name:    m.Metadata.Name,
		Version: m.Metadata.Version,
		Source:  ref,
	}
	pkg.Manifest, _ = os.ReadFile(filepath.Join(staged, providertypes.ManifestFile))

	res, err := installer.Install(ctx, pkg)
	if err != nil {
		return err
	}
	if res.AlreadyPresent {
		fmt.Printf("provider %s@%s already installed in %s\n", pkg.Name, pkg.Version, store.Root())
		return nil
	}
	fmt.Printf("installed provider %s@%s at %s\n", pkg.Name, pkg.Version, res.Installed.RootDir)
	return nil
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if entry.Type()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(dstPath, data, mode); err != nil {
			return err
		}
	}
	return nil
}
