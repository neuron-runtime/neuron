package executor

// Observer reports the lifecycle of executor resolution and installation.
//
// Observers are pure sinks: the pipeline reports progress through them but
// never depends on their behavior. A nil Observer keeps resolution silent.
//
// The executor package never prints. User-facing progress is rendered by the
// CLI layer, which implements Observer (see application/internal/cli/progress);
// this interface exists so that layer can observe without reaching into the
// pipeline.
type Observer interface {
	// Resolving reports that a Requirement is about to be resolved against
	// the registries allowed by res.Registries.
	Resolving(res Requirement)

	// AlreadyInstalled reports that the local store already satisfies res
	// with an exact installed artifact, so no registry was contacted and
	// nothing was installed.
	AlreadyInstalled(res Requirement, installed Installed)

	// Installing reports that a concrete Package is being materialized into
	// the store. pkg.Registry and pkg.Version identify the exact artifact.
	Installing(pkg Package)

	// Installed reports a completed installation. result.AlreadyPresent
	// marks an idempotent hit where the exact version was already in the
	// store and no download occurred.
	Installed(result InstallResult)
}
