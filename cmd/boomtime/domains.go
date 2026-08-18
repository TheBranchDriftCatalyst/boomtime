package main

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/github"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domain"
)

// buildDomainRegistry is the composition root's canonical domain set (gaka-zp2s).
// Registration ORDER is load-bearing: the aggregated EncryptedColumns() must match
// the pre-registry order (waka → github → amazon → hardcover) so key-rotation is
// byte-identical. Both the server (main.go) and the rotate-encryption-key command
// build from this ONE list, so a new domain is added in exactly one place.
//
// A standalone image would build a registry with only its own Module; the host
// registers them all.
func buildDomainRegistry() *domain.Registry {
	r := domain.NewRegistry()
	r.Add(boomtime.Module{}) // waka (base domain)
	r.Add(github.Module{})   // github stats
	r.Add(books.Module{})    // catalyst-books (amazon + hardcover)
	return r
}
