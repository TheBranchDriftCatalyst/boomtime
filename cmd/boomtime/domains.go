package main

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/domainreg"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/catalyst"
)

// buildDomainRegistry returns the composition root's canonical domain registry
// (boom-zp2s), built by the single source in internal/domainreg. Used by commands
// that only need the aggregated column contract (rotate-encryption-key); the server
// path uses domainreg.Build() directly so it also gets the typed late-wire handles.
//
// A standalone image builds its own single-domain wiring; the host registers them all.
func buildDomainRegistry() *catalyst.Registry {
	return domainreg.Build().Registry
}
