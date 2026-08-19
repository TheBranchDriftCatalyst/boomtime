// register_domains_test.go — test-only blank imports (gaka-zp2s). The domain-coupled
// web-run commands ("hardcover dedup-reads", "backfill github-stats") now live in the
// books / github domains and register themselves into climeta's allowlist via their
// packages' init(). climeta's own spec/registry tests assert the full command set, so
// this external test package pulls those domains in to trigger their registration.
//
// This is an EXTERNAL test package (climeta_test) importing packages that import climeta
// — the canonical way to break what would otherwise be an import cycle; climeta's
// production code imports neither domain.
package climeta_test

import (
	_ "github.com/TheBranchDriftCatalyst/boomtime/internal/books"
	_ "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/github"
)
