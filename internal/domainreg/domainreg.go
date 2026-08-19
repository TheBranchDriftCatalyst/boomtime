// Package domainreg is the ONE place the boomtime host's domain set is instantiated
// (gaka-zp2s). It builds the catalyst.Registry the composition root threads into the
// server (route + admin wiring) and the jobs block (kind + schedule wiring), plus typed
// handles to the domain Modules the host late-wires after the jobs subsystem is up.
//
// It lives OUTSIDE internal/server so the server package takes the registry as a
// parameter instead of importing the domains — a step toward folding server into
// internal/shared. cmd/boomtime and the server package's route-drift test both build
// from here, so there is exactly one module list (no duplicate instantiation to drift).
//
// A standalone image (cmd/catalyst-books) builds its own single-domain wiring directly
// and does NOT use this package.
package domainreg

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/github"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/catalyst"
)

// Set bundles the composition registry with typed handles to the domain Modules the
// host late-wires. Registration ORDER is load-bearing for the aggregated
// EncryptedColumns() (waka → github → amazon → hardcover) so key-rotation stays
// byte-identical.
type Set struct {
	Registry *catalyst.Registry
	// Boomtime is the same *boomtime.Module instance stored in Registry — the host
	// late-wires its import worker, label-images worker, image-job queue, and jobs
	// store onto it after those subsystems initialize.
	Boomtime *boomtime.Module
	// Books is the same *books.Module instance stored in Registry — the host wires its
	// job enqueuer + inline Hardcover push onto it after the jobs subsystem is built.
	Books *books.Module
}

// Build constructs the canonical boomtime domain set. A new domain is added here in
// exactly one place and immediately participates in route/admin/job/column wiring.
func Build() Set {
	boomtimeMod := boomtime.New()
	booksMod := books.New()
	r := catalyst.NewRegistry()
	r.Add(boomtimeMod)     // waka (base domain)
	r.Add(github.Module{}) // github stats
	r.Add(booksMod)        // catalyst-books (amazon + hardcover)
	return Set{Registry: r, Boomtime: boomtimeMod, Books: booksMod}
}
