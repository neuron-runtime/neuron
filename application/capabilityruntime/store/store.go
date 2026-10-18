// Package store implements the installed capability runtime catalog used by the
// application/capabilityruntime resolver and installer. An installed capability runtime is an
// immutable directory that has been fully verified and atomically committed.
// The store never performs resolution or downloading; it only persists and
// serves installed artifacts.
//
// Layout:
//
//	<root>/
//	    <owner>/<...segments>/<version>/<digest>/
//	        runtime.json
//	        install.json
//	        <extracted artifact files>
//
// The layout is content-addressed: the digest directory holds exactly the
// artifact the install.json record commits to, so identity derives the path.
// Installs left flat directly under <version>/ by pre-content-addressed
// versions of the store are migrated in place on first read (see migrate.go).
//
// The Store interface consumed by the resolve/install pipeline is declared in
// application/capabilityruntime (consumer side); this package supplies the concrete
// FileassemblyStore and the install.json record helpers.
package store
