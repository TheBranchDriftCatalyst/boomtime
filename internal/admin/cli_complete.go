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
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
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
func (h *Handler) CLIComplete(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	var req cliCompleteRequest
	if aerr := apihelpers.BindJSONWithLimit(c, &req, cliCompleteBodyLimit); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	_, entry, aerr := h.cliLookup(req.Command)
	if aerr != nil {
		h.cliAuditDenial(owner, req.Command, "complete: unknown or unavailable command")
		return apihelpers.RespondErr(c, aerr)
	}

	// Pick the completer: named flag's, else the positional ArgCompleter.
	// A param with no completer yields an empty suggestion list (the spec's
	// Completable=false already told the FE not to ask).
	var fn cobra.CompletionFunc
	if req.Flag != "" {
		fn = entry.FlagCompleters[req.Flag]
	} else {
		fn = entry.ArgCompleter
	}

	suggestions, directive := climeta.InvokeCompleter(fn, req.Args, req.ToComplete)
	return c.JSON(http.StatusOK, cliCompleteResponse{Suggestions: suggestions, Directive: directive})
}
