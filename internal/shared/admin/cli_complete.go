// cli_complete.go — POST /api/v1/admin/cli/complete (admin CLI-runner
// backend, BOOM_FEATURE_ADMIN_CLI). Serves autocomplete suggestions by
// calling the registry's cobra completion funcs DIRECTLY — never cobra's
// hidden `__complete` dispatch, which would mean assembling an argv from
// user input and depending on version-fragile internals. Prior positional
// values thread through so contextual completers behave exactly as under a
// shell <TAB>. A panicking completer is recovered inside
// climeta.InvokeCompleter and surfaces as an empty result with the error
// directive — it can never crash the request.
package admin

import (
	"github.com/labstack/echo/v5"
	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/climeta"
)

// cliCompleteBodyLimit caps the complete request body — a command path, a
// few prior args, and a prefix.
const cliCompleteBodyLimit = 16 << 10

// cliCompleteRequest is the POST /api/v1/admin/cli/complete body. When Flag
// is set the flag's completer runs; otherwise the command's positional
// ArgCompleter runs with Args as the prior positional values.
type cliCompleteRequest struct {
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	Flag       string   `json:"flag"`
	ToComplete string   `json:"toComplete"`
}

// cliCompleteResponse mirrors climeta.InvokeCompleter's wire shapes.
type cliCompleteResponse struct {
	Suggestions []climeta.Suggestion `json:"suggestions"`
	Directive   climeta.Directive    `json:"directive"`
}

// CLIComplete serves autocomplete suggestions for one allowlisted command's
// positional argument or flag value.
//
// TYPED SEAM NOTE (routes.go registers this via apiroute.POSTNoBody): the body
// is bound HERE, not by the seam's POST form — same reasoning as CLIRun.
// requireAdmin must precede the body read, and the seam's fixed BodyLimitSmall
// (4 KiB) would shrink cliCompleteBodyLimit (16 KiB).
func (h *Handler) CLIComplete(c *echo.Context) (cliCompleteResponse, error) {
	var out cliCompleteResponse
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return out, aerr
	}

	var req cliCompleteRequest
	if berr := apihelpers.BindJSONWithLimit(c, &req, cliCompleteBodyLimit); berr != nil {
		return out, berr
	}

	_, entry, aerr := h.cliLookup(req.Command)
	if aerr != nil {
		h.cliAuditDenial(owner, req.Command, "complete: unknown or unavailable command")
		return out, aerr
	}

	// Pick the completion source: named flag's, else the positional one.
	// A param with neither yields an empty suggestion list (the spec's
	// Completable=false already told the FE not to ask).
	//
	// POOL REUSE (QA fix): prefer the registry's DBLister form, run against
	// the server's existing pool via climeta.CompleteWithDB — the cobra
	// completer funcs self-open a fresh bounded connection per call (right
	// for shell <TAB>, wasteful per HTTP request). The InvokeCompleter
	// fallback remains for future pure/contextual completers that have no
	// lister form (e.g. enum completers), which open no connection at all
	// or accept the self-open cost explicitly.
	var lister climeta.DBLister
	var fn cobra.CompletionFunc
	if req.Flag != "" {
		lister = entry.FlagListers[req.Flag]
		fn = entry.FlagCompleters[req.Flag]
	} else {
		lister = entry.ArgLister
		fn = entry.ArgCompleter
	}

	var suggestions []climeta.Suggestion
	var directive climeta.Directive
	if lister != nil {
		suggestions, directive = climeta.CompleteWithDB(c.Request().Context(), h.DB, lister, req.ToComplete)
	} else {
		suggestions, directive = climeta.InvokeCompleter(fn, req.Args, req.ToComplete)
	}
	return cliCompleteResponse{Suggestions: suggestions, Directive: directive}, nil
}
