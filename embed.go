// Package boomtime exposes assets that must be embedded from the module root.
//
// CHANGELOG.md is generated at the repository root (by `task changelog`, which
// shells out to git-cliff — see cliff.toml) and embedded here so the running
// server can serve it at /api/v1/changelog. Keeping the embed at the module
// root avoids any copy-into-package dance and guarantees the file is always
// fresh when built.
//
// Gotcha: `go:embed` refuses to compile if the target file is missing. If a
// fresh clone hits this before ever running `task changelog`, run:
//
//	task changelog
//
// (or just `git cliff -o CHANGELOG.md`). CI + the Dockerfile always start from
// a checked-in CHANGELOG.md, so the compile is safe there.
package boomtime

import _ "embed"

// ChangelogMD is the Markdown changelog produced by git-cliff, embedded at
// build time. Served verbatim to the FE, which parses it in the browser.
//
//go:embed CHANGELOG.md
var ChangelogMD []byte
