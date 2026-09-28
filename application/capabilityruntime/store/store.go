// Package store implements the installed capability runtime catalog used by the
// application/capabilityruntime resolver and installer. An installed capability runtime is an
// immutable directory that has been fully verified and atomically committed.
// The store never performs resolution or downloading; it only persists and
// serves installed artifacts.
//
// Layout:
//
//	<root>/
//	    <owner>/<...segments>/<version>/
//	        runtime.json
//	        install.json
//	        <extracted artifact files>
//
// The Store interface consumed by the resolve/install pipeline is declared in
// application/capabilityruntime (consumer side); this package supplies the concrete
// FileassemblyStore and the install.json record helpers.
package store
