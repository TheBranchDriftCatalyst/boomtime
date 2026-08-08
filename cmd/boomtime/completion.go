package main

// Transparent shell-completion layer (gaka-0oe.10). The generator + concrete
// completion funcs moved verbatim to internal/climeta so the admin CLI-runner
// (BOOM_FEATURE_ADMIN_CLI) can drive the SAME completers over HTTP; these
// aliases keep every cmd/boomtime call site unchanged. `boomtime completion
// zsh` and the hidden `__complete` dispatch behave identically to before the
// move — cobra sees the same func values.
import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
)

var (
	completeUsernames        = climeta.CompleteUsernames
	completeLabelIds         = climeta.CompleteLabelIDs
	completeUsernameThenRole = climeta.CompleteUsernameThenRole
)
