package provider

import (
	"context"
	"fmt"
)

// Resolver resolves provider references into installed providers.
type Resolver struct {
	Store Store
}

// Resolve returns the installed provider satisfying the name/version.
func (r *Resolver) Resolve(ctx context.Context, name, version string) (*Installed, error) {
	if r.Store == nil {
		return nil, fmt.Errorf("provider store is nil")
	}
	return r.Store.Get(ctx, name, version)
}
