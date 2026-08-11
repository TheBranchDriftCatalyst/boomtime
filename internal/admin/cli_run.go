// cli_run.go — POST /api/v1/admin/cli/run (admin CLI-runner backend,
// BOOM_FEATURE_ADMIN_CLI). Runs ONE allowlisted command synchronously,
// in-process, with typed args. There is NO subprocess anywhere on this path:
// "running a command" means calling the same Go function the cobra RunE
// calls, so no argv is ever assembled from user input (the repo-wide
// zero-subprocess invariant holds).
//
// Guard stack, in order:
//  1. route exists only when Cfg.FeatureAdminCLI (routes.go)
//  2. RequireCap(CapAdmin) route middleware (defense-in-depth)
//  3. requireAdmin — the BOOM_ADMIN_USERS hard gate, BEFORE the body is read
//  4. registry ∩ annotation ∩ availability lookup (404 on any miss)
//  5. typed bind/validate (unknown keys rejected, values coerced by type)
//  6. mutating commands: dry-run defaults ON; applying requires
//     confirm == command (mirror of DBImport's ?confirm=replace-all-data)
//  7. bounded context, captured output, length cap, audit log
//
// OUTPUT IS DELIBERATELY NOT SCRUBBED/REDACTED — this is a decision, not an
// omission. The CLI runner is an admin-only operator console: the captured
// buffer returns to the authenticated admin who ran the command, so there is
// no public boundary and the widget public-safe scrubber
// (internal/widget/scrub.go, which strips curation-hidden names for PUBLIC
// exposure) does not apply here. Secret-safety is handled STRUCTURALLY by
// the allowlist instead: secret-emitting commands (rotate-encryption-key,
// create-token, create-user) are excluded from the registry, and every
// exposed command is secret-safe by construction (the github backfill never
// emits its token — see internal/github/sync.go; user list/show emit
// usernames/roles/caps, not credentials). The one masking rule lives in the
// AUDIT LOG: a flag the spec marks Secret is logged as "***" (maskFlags),
// matching internal/db/observability.go's redactArgs convention.
package admin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
)

const (
	// cliRunBodyLimit caps the run request body — a command name + a small
	// flags object; anything bigger is garbage.
	cliRunBodyLimit = 64 << 10
	// cliRunTimeout bounds a synchronous run. The registered backfills are
	// single-transaction DB work that completes well inside this on any
	// realistic dataset; a hung run must not pin the request forever.
	cliRunTimeout = 5 * time.Minute
	// cliMaxOutputBytes caps the captured output so a pathological run can't
	// balloon the response; truncation is marked inline in the output.
	cliMaxOutputBytes = 64 << 10
)

// cappedWriter is the bounded sink Invoke writes into: it accepts at most
// max bytes and DISCARDS the rest, so memory is bounded DURING the run — a
// future unbounded-output command cannot buffer without limit before a
// post-hoc truncation. Write always reports the full length consumed so
// fmt.Fprintf inside command bodies never sees a short-write error (the
// command keeps running; only its output is capped).
type cappedWriter struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	room := w.max - w.buf.Len()
	if room <= 0 {
		if len(p) > 0 {
			w.truncated = true
		}
		return len(p), nil
	}
	if len(p) > room {
		w.buf.Write(p[:room])
		w.truncated = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

// cliRunRequest is the POST /api/v1/admin/cli/run body. Flags carries EVERY
// param keyed by name — positional params (e.g. user show's username) are
// sent in the same object; the binder routes them by the spec's Positional
// marker. Confirm must equal the command path to apply a mutating command.
type cliRunRequest struct {
	Command string         `json:"command"`
	Flags   map[string]any `json:"flags"`
	Confirm string         `json:"confirm"`
}

// cliRunResponse is the run result envelope. ExitError is "" on success and
// carries the error text otherwise — mirroring the CLI's stderr + non-zero
// exit. HTTP status is 200 for both: a failing command is a valid run
// outcome, not a protocol error.
type cliRunResponse struct {
	OK         bool   `json:"ok"`
	Output     string `json:"output"`
	ExitError  string `json:"exitError"`
	DryRun     bool   `json:"dryRun"`
	DurationMs int64  `json:"durationMs"`
}

// CLIRun executes one allowlisted command in-process and returns its
// captured output.
func (h *Handler) CLIRun(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	var req cliRunRequest
	if aerr := apihelpers.BindJSONWithLimit(c, &req, cliRunBodyLimit); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	spec, entry, args, dryRun, aerr := h.cliResolve(owner, req)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), cliRunTimeout)
	defer cancel()

	out := &cappedWriter{max: cliMaxOutputBytes}
	start := time.Now()
	runErr := h.invokeCLI(ctx, entry, args, out)
	duration := time.Since(start)

	output := out.buf.String()
	if out.truncated {
		output += "\n… [output truncated]"
	}

	exitError := ""
	outcome := "ok"
	if runErr != nil {
		exitError = runErr.Error()
		outcome = "error"
	}

	h.Logger.Info("admin cli run",
		"actor", owner,
		"command", req.Command,
		"flags", maskFlags(spec, req.Flags),
		"classification", entry.Classification,
		"dryRun", dryRun,
		"outcome", outcome,
		"durationMs", duration.Milliseconds(),
	)

	return c.JSON(http.StatusOK, cliRunResponse{
		OK:         runErr == nil,
		Output:     output,
		ExitError:  exitError,
		DryRun:     dryRun,
		DurationMs: duration.Milliseconds(),
	})
}

// cliResolve runs the full allowlist → bind → dry-run/confirm gate shared by
// the sync (CLIRun) and streaming (CLIRunWS) paths. On any refusal it audits
// the denial and returns the *apierr.Error to surface. `applying` is folded in:
// a mutating command with dry-run off and a mismatched confirm is rejected here.
func (h *Handler) cliResolve(owner string, req cliRunRequest) (climeta.CommandSpec, climeta.RegistryEntry, climeta.RunArgs, bool, *apierr.Error) {
	spec, entry, aerr := h.cliLookup(req.Command)
	if aerr != nil {
		h.cliAuditDenial(owner, req.Command, "unknown or unavailable command")
		return climeta.CommandSpec{}, climeta.RegistryEntry{}, climeta.RunArgs{}, false, aerr
	}
	// Defense-in-depth: nothing destructive is ever registered, but if an entry
	// drifted in, refuse it outright rather than trusting confirm.
	if entry.Classification == climeta.ClassDestructive {
		h.cliAuditDenial(owner, req.Command, "destructive class refused")
		return climeta.CommandSpec{}, climeta.RegistryEntry{}, climeta.RunArgs{}, false,
			apierr.Forbidden("destructive commands cannot be run from the web")
	}

	args, err := climeta.BindRunArgs(spec, req.Flags)
	if err != nil {
		h.cliAuditDenial(owner, req.Command, "bind: "+err.Error())
		return climeta.CommandSpec{}, climeta.RegistryEntry{}, climeta.RunArgs{}, false,
			apierr.BadRequest(err.Error())
	}

	// dry-run semantics: supported ⇒ the binder defaulted it TRUE when absent;
	// not supported ⇒ every run of a mutating command is an apply.
	dryRun := entry.DryRunSupported && args.Bool(climeta.DryRunFlag)
	applying := entry.Classification == climeta.ClassMutating && !dryRun
	if applying && req.Confirm != req.Command {
		h.cliAuditDenial(owner, req.Command, "missing confirm sentinel for mutating apply")
		return climeta.CommandSpec{}, climeta.RegistryEntry{}, climeta.RunArgs{}, false,
			apierr.BadRequest(fmt.Sprintf(
				"applying %q requires confirm=%q (omit dry-run:false to preview instead)",
				req.Command, req.Command))
	}
	return spec, entry, args, dryRun, nil
}

// invokeCLI calls the registered in-process command body with a recover guard
// so a panicking invoker surfaces as a run error instead of crashing the
// request/stream. Shared by the sync + WS paths.
func (h *Handler) invokeCLI(ctx context.Context, entry climeta.RegistryEntry, args climeta.RunArgs, out io.Writer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("command panicked: %v", r)
		}
	}()
	return entry.Invoke(ctx, h.DB, args, out)
}

// cliLookup resolves a command path through the full allowlist chain:
// registry entry present, available under the current config, AND its
// command def carries a matching web annotation (BuildSpec enforces that).
// Every miss is the same 404 — absent, unavailable, and unannotated are
// deliberately indistinguishable to the caller.
func (h *Handler) cliLookup(command string) (climeta.CommandSpec, climeta.RegistryEntry, *apierr.Error) {
	entry, ok := climeta.Registry()[command]
	if !ok {
		return climeta.CommandSpec{}, climeta.RegistryEntry{}, apierr.NotFound("unknown command")
	}
	if entry.Available != nil && !entry.Available(h.Cfg) {
		return climeta.CommandSpec{}, climeta.RegistryEntry{}, apierr.NotFound("unknown command")
	}
	spec, ok := climeta.BuildSpec(command, entry)
	if !ok {
		return climeta.CommandSpec{}, climeta.RegistryEntry{}, apierr.NotFound("unknown command")
	}
	return spec, entry, nil
}

// cliAuditDenial logs a refused run/complete attempt. Flag VALUES are never
// logged here (the request may not even have bound) — only the actor, the
// command they asked for, and why it was refused.
func (h *Handler) cliAuditDenial(actor, command, reason string) {
	h.Logger.Warn("admin cli denied",
		"actor", actor,
		"command", command,
		"reason", reason,
	)
}

// maskFlags renders the request flags for the audit log with the values of
// Secret-marked params replaced by "***" (the same masking convention as
// internal/db/observability.go's redactArgs). Phase-1 commands have no
// secret flags — this is forward-proofing so a future registry addition
// with a credential-shaped param never lands its value in the server log.
func maskFlags(spec climeta.CommandSpec, raw map[string]any) map[string]any {
	secret := map[string]bool{}
	for _, p := range spec.Params {
		if p.Secret {
			secret[p.Name] = true
		}
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if secret[k] {
			out[k] = "***"
			continue
		}
		out[k] = v
	}
	return out
}
