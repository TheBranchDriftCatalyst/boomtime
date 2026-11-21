// Package books is what remains of the catalyst-books domain after it was
// deprecated (2026-11-19) in favour of the BookOrbit fork at ../bookorbit.
// See docs/design/books-domain-deprecated.md.
//
// THIS MODULE NOW CONTRIBUTES COLUMNS AND NOTHING ELSE.
//
// The HTTP surface (internal/books/api), the admin surface (internal/books/admin),
// the job kinds and schedules (internal/books/jobs) and the standalone binary
// (cmd/catalyst-books) were all removed in boom-sire. Boomtime no longer syncs
// Audible, polls Kindle, pushes to Hardcover, or liberates anything — the fork
// does that, and it is registered as its own device on the same Amazon account.
// Two sweepers against one account is what liberate/license.go warns about.
//
// WHY THIS FILE STILL EXISTS, AND WHY DELETING IT WOULD BE A SILENT REGRESSION:
// the books tables still hold per-user AES-GCM ciphertext in production — the
// Amazon device credential and the Hardcover key. `Registry.EncryptedColumns`
// and `Registry.BackupColumns` (internal/shared/catalyst) aggregate over EVERY
// registered module WITHOUT consulting Enabled(), which is deliberate: turning a
// feature off must not strand its secrets. So this module staying registered in
// internal/domainreg is the only thing keeping
//
//   - `boomtime rotate-encryption-key` rotating those columns, and
//   - the whole-DB export including them,
//
// both of which must keep working for as long as the ciphertext exists. Drop the
// module and rotation silently stops covering those rows: it would not error, it
// would just quietly leave them on the old key and strand them at the next
// rotation. Registration ORDER also stays as domainreg documents it
// (waka → github → amazon → hardcover) so rotation stays byte-identical.
//
// Everything else is BaseModule's no-op default. When Kindle (CAT-0t4) and
// liberation (CAT-3mq) are ported in the fork and the books tables are actually
// dropped, this file and the remaining protocol packages go together — see
// boom-zrhc.
package books

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/catalyst"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/domaincols"
)

// Module implements catalyst.Module for the deprecated catalyst-books domain. It is
// now STATELESS — there is no handler to stash and nothing to late-wire, so the
// host needs none of the old Set*/Wire* setters. Still used via *Module so the
// registry stores a pointer and domainreg's typed handle keeps its shape.
type Module struct {
	catalyst.BaseModule
}

// New constructs the books Module. Nothing is allocated lazily any more; there is
// nothing left to allocate.
func New() *Module { return &Module{} }

func (*Module) Name() string { return "books" }

// Enabled still reports BOOM_FEATURE_BOOKS so the domain's own gate keeps its
// meaning, but with no routes, admin surface, jobs or schedules left there is
// nothing for it to gate. It does NOT affect column aggregation — see the package
// comment. The flag is "false" in the talos00 overlay as of 2026-11-20.
func (*Module) Enabled(cfg *config.Config) bool { return cfg != nil && cfg.FeatureBooks }

// EncryptedColumns: the Amazon device credential + Hardcover key (both per-user
// AES-GCM secrets). Derived from the single-source domains registry. This is the
// load-bearing half of this file.
func (*Module) EncryptedColumns() []domaincols.EncryptedColumn {
	return domaincols.EncryptedColumnsFor("amazon", "hardcover")
}

// BackupColumns: the same secrets + their status/metadata siblings, included in the
// whole-DB export.
func (*Module) BackupColumns() []domaincols.BackupColumns {
	return domaincols.BackupColumnsFor("amazon", "hardcover")
}
