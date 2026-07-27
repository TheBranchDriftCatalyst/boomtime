// backfill.go: `boomtime backfill git` — laptop-side CLI that walks
// local git repos and streams synthetic Wakatime-style heartbeats to
// the boomtime API (gaka-vh8).
//
// Design:
//   - Walk repos under --root (skipping vendored / cache dirs; see
//     internal/backfill/git.WalkRepos).
//   - For each repo: enqueue a job with the server (POST /admin/backfill
//     /jobs), then scan commits, cluster into sessions, materialize
//     heartbeats, and POST to /admin/backfill/jobs/:id/heartbeats (or
//     /preview if --dry-run) in modest batches. Close the job with a
//     PATCH once the repo is drained.
//   - Config: the server-side backfill_config supplies defaults; CLI
//     flags override.
//
// Auth: bearer token (--token / $BOOM_API_TOKEN) — the CLI runs on the
// operator's laptop, the server is prod.
//
// No shell exec: repo access is entirely via go-git. Discovery is a
// pure filepath.WalkDir. This lets the CLI run in restricted /
// sandboxed environments where `git` may not be on PATH.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"encoding/base64"
	"strings"
	"time"

	backfillgit "github.com/TheBranchDriftCatalyst/boomtime/internal/backfill/git"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/spf13/cobra"
)

// backfillCmd registers `boomtime backfill` and its `git` subcommand.
func backfillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Backfill boomtime with historical activity (git, ...)",
	}
	cmd.AddCommand(backfillGitCmd())
	return cmd
}

// backfillGitCmd is the CLI for the git-history backfill flow.
func backfillGitCmd() *cobra.Command {
	var (
		root        string
		emails      []string
		apiBase     string
		token       string
		dryRun      bool
		skipPats    []string
		sinceRaw    string
		untilRaw    string
		concurrency int
		batchSize   int
	)
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Backfill from local git repositories",
		Long: `Walk .git repos under --root, cluster author-matched commits into
sessions, materialize synthetic Wakatime-style heartbeats, and stream
them to a boomtime admin API.

The command inherits its clustering config (gap, lead/tail, HB rate,
author allowlist) from the server-side backfill_config. --emails and
similar flags override those defaults for this run only.

Auth: pass --token (or set $BOOM_API_TOKEN) with an admin's API token.
Real Wakatime data is protected via a per-session overlap check: any
session that overlaps a real heartbeat is dropped server-side.

Example:

  boomtime backfill git \
    --root ~/code \
    --emails me@example.com,me@work.com \
    --api https://boomtime.example.com \
    --token $BOOM_API_TOKEN
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(root) == "" {
				return errors.New("--root is required")
			}
			absRoot, err := filepath.Abs(root)
			if err != nil {
				return fmt.Errorf("resolve --root: %w", err)
			}
			if apiBase == "" {
				apiBase = strings.TrimSpace(os.Getenv("BOOM_API_URL"))
			}
			if apiBase == "" {
				return errors.New("--api is required (or set BOOM_API_URL)")
			}
			if token == "" {
				token = strings.TrimSpace(os.Getenv("BOOM_API_TOKEN"))
			}
			if token == "" {
				return errors.New("--token is required (or set BOOM_API_TOKEN)")
			}

			// Parse --since / --until as YYYY-MM-DD (UTC). Empty ok.
			var since, until time.Time
			if sinceRaw != "" {
				t, err := time.Parse("2006-01-02", sinceRaw)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				since = t.UTC()
			}
			if untilRaw != "" {
				t, err := time.Parse("2006-01-02", untilRaw)
				if err != nil {
					return fmt.Errorf("--until: %w", err)
				}
				until = t.UTC().Add(24 * time.Hour).Add(-time.Second)
			}

			// Fetch server config so we inherit tunables the operator
			// dialed in via the UI. Ignore fetch errors — the CLI
			// can still function against a fresh server that hasn't
			// stored anything yet (the endpoint returns defaults).
			cli := &apiClient{base: strings.TrimRight(apiBase, "/"), token: token, http: http.DefaultClient}
			svrCfg, err := cli.fetchConfig(cmd.Context())
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not fetch server config (%v); using bare defaults\n", err)
				svrCfg = db.BackfillConfig{
					ClusterGapSec:     1800,
					PreCommitLeadSec:  900,
					PostCommitTailSec: 300,
					HeartbeatRateSec:  120,
					SourceTag:         "backfill:git",
					AuthorEmails:      nil,
					LangMap:           map[string]string{},
				}
			}

			// Merge flags over server config.
			finalEmails := svrCfg.AuthorEmails
			if len(emails) > 0 {
				finalEmails = emails
			}
			if len(finalEmails) == 0 {
				return errors.New("no --emails and no server-side author_emails; refusing to run (would attribute every commit's time regardless of author)")
			}

			estCfg := backfillgit.EstimatorConfig{
				ClusterGap:     time.Duration(svrCfg.ClusterGapSec) * time.Second,
				PreCommitLead:  time.Duration(svrCfg.PreCommitLeadSec) * time.Second,
				PostCommitTail: time.Duration(svrCfg.PostCommitTailSec) * time.Second,
				HeartbeatRate:  time.Duration(svrCfg.HeartbeatRateSec) * time.Second,
				AuthorEmails:   finalEmails,
				Since:          since,
				Until:          until,
				LangMap:        svrCfg.LangMap,
			}

			// Repo discovery.
			repos, err := backfillgit.WalkRepos(absRoot)
			if err != nil {
				return fmt.Errorf("walk repos: %w", err)
			}
			// Apply --skip-repo glob filters (matched against the abs
			// path AND the basename, both for convenience).
			if len(skipPats) > 0 {
				kept := repos[:0]
				for _, r := range repos {
					if skipMatch(r, skipPats) {
						fmt.Printf("[skip] %s\n", r)
						continue
					}
					kept = append(kept, r)
				}
				repos = kept
			}
			if len(repos) == 0 {
				fmt.Println("no git repos found under --root")
				return nil
			}

			// concurrency is currently capped at 1 for gentleness; the
			// flag is threaded so a future parallel run doesn't need a
			// wire-shape change. See file-level doc for rationale.
			if concurrency < 1 {
				concurrency = 1
			}
			if concurrency > 1 {
				fmt.Fprintf(os.Stderr, "note: --concurrency > 1 is not implemented yet, running sequentially\n")
			}

			fmt.Printf("found %d repo(s) under %s\n", len(repos), absRoot)
			fmt.Printf("config: gap=%s lead=%s tail=%s rate=%s emails=%v dry-run=%v\n",
				estCfg.ClusterGap, estCfg.PreCommitLead,
				estCfg.PostCommitTail, estCfg.HeartbeatRate,
				estCfg.AuthorEmails, dryRun)

			ctx := cmd.Context()
			totals := runTotals{}
			for i, path := range repos {
				fmt.Printf("[%d/%d] %s\n", i+1, len(repos), path)
				repoTotals, err := runBackfillOnRepo(ctx, cli, path, estCfg, batchSize, dryRun)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  error: %v\n", err)
					totals.errored++
					continue
				}
				totals.commits += repoTotals.commits
				totals.sessions += repoTotals.sessions
				totals.written += repoTotals.written
				totals.skipped += repoTotals.skipped
				totals.repos++
				fmt.Printf("  commits=%d sessions=%d written=%d skipped=%d\n",
					repoTotals.commits, repoTotals.sessions, repoTotals.written, repoTotals.skipped)
			}
			fmt.Println("---")
			fmt.Printf("SUMMARY: repos=%d errored=%d commits=%d sessions=%d written=%d skipped=%d\n",
				totals.repos, totals.errored, totals.commits, totals.sessions, totals.written, totals.skipped)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "root directory to walk (required)")
	cmd.Flags().StringSliceVar(&emails, "emails", nil, "comma-separated author email allowlist (falls back to server config)")
	cmd.Flags().StringVar(&apiBase, "api", "", "boomtime API base URL (or $BOOM_API_URL)")
	cmd.Flags().StringVar(&token, "token", "", "admin API bearer token (or $BOOM_API_TOKEN)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview overlap only, no writes")
	cmd.Flags().StringSliceVar(&skipPats, "skip-repo", nil, "glob(s) to skip; repeatable")
	cmd.Flags().StringVar(&sinceRaw, "since", "", "only consider commits on/after YYYY-MM-DD")
	cmd.Flags().StringVar(&untilRaw, "until", "", "only consider commits on/before YYYY-MM-DD")
	cmd.Flags().IntVar(&concurrency, "concurrency", 1, "parallel repo scans (currently sequential)")
	cmd.Flags().IntVar(&batchSize, "batch-size", 50, "sessions per HTTP batch")
	return cmd
}

// runTotals accumulates cross-repo counters for the SUMMARY line.
type runTotals struct {
	repos    int
	errored  int
	commits  int
	sessions int
	written  int
	skipped  int
}

// skipMatch returns true if any glob in pats matches the abs path or
// its basename.
func skipMatch(path string, pats []string) bool {
	base := filepath.Base(path)
	for _, p := range pats {
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
	}
	return false
}

// runBackfillOnRepo drives one repo end-to-end: open, scan, cluster,
// enqueue job, POST batches, close job. On any HTTP or repo error it
// tries to close the job with status=error before returning.
func runBackfillOnRepo(ctx context.Context, cli *apiClient, repoPath string, cfg backfillgit.EstimatorConfig, batchSize int, dryRun bool) (runTotals, error) {
	t := runTotals{}
	scanner, err := backfillgit.NewScanner(repoPath, cfg)
	if err != nil {
		return t, fmt.Errorf("open repo: %w", err)
	}
	repoName := scanner.RepoName()

	// Collect commits (bounded — one pass, then cluster). For
	// enormous repos (100k+ commits) we may want to stream/chunk;
	// today the simplicity wins.
	var commits []backfillgit.Commit
	for c, err := range scanner.Iter(ctx) {
		if err != nil {
			return t, fmt.Errorf("scan: %w", err)
		}
		commits = append(commits, c)
	}
	t.commits = len(commits)
	if len(commits) == 0 {
		fmt.Printf("  (no author-matched commits)\n")
		return t, nil
	}
	sessions := backfillgit.Cluster(commits, cfg)
	t.sessions = len(sessions)

	// Enqueue job on the server so the admin UI shows this repo mid-run.
	jobID, err := cli.enqueueJob(ctx, repoName, repoPath, len(commits))
	if err != nil {
		return t, fmt.Errorf("enqueue job: %w", err)
	}

	// Push in batches. On any batch failure, PATCH the job to error and
	// abort — better to surface a failure loudly than silently continue.
	var written, skipped int
	for i := 0; i < len(sessions); i += batchSize {
		hi := i + batchSize
		if hi > len(sessions) {
			hi = len(sessions)
		}
		batch := sessions[i:hi]
		payload := buildBatchPayload(batch, cfg.HeartbeatRate)
		res, err := cli.pushBatch(ctx, jobID, payload, dryRun)
		if err != nil {
			_ = cli.patchJobError(ctx, jobID, err.Error())
			return t, fmt.Errorf("push batch [%d,%d): %w", i, hi, err)
		}
		written += res.AcceptedHeartbeats
		skipped += res.SkippedHeartbeats
	}
	t.written = written
	t.skipped = skipped

	if err := cli.patchJobDone(ctx, jobID, len(commits), written, skipped); err != nil {
		return t, fmt.Errorf("close job: %w", err)
	}
	return t, nil
}

// buildBatchPayload converts a slice of Sessions into the
// heartbeatsBatchReq wire shape the /jobs/:id/heartbeats endpoint
// expects. Deliberately drops the language pointer when empty so a
// null field lands on the wire as omitted rather than "null".
func buildBatchPayload(sessions []backfillgit.Session, rate time.Duration) heartbeatsBatchWire {
	out := heartbeatsBatchWire{Sessions: make([]sessionWire, 0, len(sessions))}
	sourceTag := "backfill:git" // Server overrides on insert — this is a placeholder.
	for _, s := range sessions {
		hbs := backfillgit.Materialize(s, sourceTag, rate)
		payload := make([]model.HeartbeatPayload, 0, len(hbs))
		for _, hb := range hbs {
			p := model.HeartbeatPayload{
				Entity:    hb.Entity,
				Type:      model.EntityType(hb.Type),
				Category:  strPtr(hb.Category),
				Project:   hb.Project,
				Language:  hb.Language,
				Sender:    hb.Sender,
				UserAgent: hb.UserAgent,
				TimeSent:  hb.Time,
			}
			payload = append(payload, p)
		}
		out.Sessions = append(out.Sessions, sessionWire{
			Start:      s.Start,
			End:        s.End,
			Heartbeats: payload,
		})
	}
	return out
}

func strPtr(s string) *string { return &s }

// ---- API client -----------------------------------------------------

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

type heartbeatsBatchWire struct {
	Sessions []sessionWire `json:"sessions"`
}

type sessionWire struct {
	Start      time.Time                `json:"start"`
	End        time.Time                `json:"end"`
	Heartbeats []model.HeartbeatPayload `json:"heartbeats"`
}

// fetchConfig returns the server-persisted backfill config for the
// authenticated admin.
func (c *apiClient) fetchConfig(ctx context.Context) (db.BackfillConfig, error) {
	var cfg db.BackfillConfig
	if err := c.doJSON(ctx, "GET", "/api/v1/admin/backfill/config", nil, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// enqueueJob POSTs a new job and returns its ID.
func (c *apiClient) enqueueJob(ctx context.Context, repoName, repoPath string, total int) (string, error) {
	body := map[string]any{
		"repoName":     repoName,
		"repoPath":     repoPath,
		"totalCommits": total,
	}
	var out struct {
		JobID string `json:"jobId"`
	}
	if err := c.doJSON(ctx, "POST", "/api/v1/admin/backfill/jobs", body, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

// pushBatch POSTs one heartbeats batch to the job. When dryRun=true the
// endpoint changes to /preview (server does overlap check only).
func (c *apiClient) pushBatch(ctx context.Context, jobID string, batch heartbeatsBatchWire, dryRun bool) (db.BackfillResult, error) {
	var res db.BackfillResult
	sub := "heartbeats"
	if dryRun {
		sub = "preview"
	}
	path := "/api/v1/admin/backfill/jobs/" + url.PathEscape(jobID) + "/" + sub
	if err := c.doJSON(ctx, "POST", path, batch, &res); err != nil {
		return res, err
	}
	return res, nil
}

// patchJobDone marks the job done with final counters.
func (c *apiClient) patchJobDone(ctx context.Context, jobID string, processed, written, skipped int) error {
	body := map[string]any{
		"status":    "done",
		"processed": processed,
		"written":   written,
		"skipped":   skipped,
	}
	return c.doJSON(ctx, "PATCH", "/api/v1/admin/backfill/jobs/"+url.PathEscape(jobID), body, nil)
}

// patchJobError marks the job error with a message.
func (c *apiClient) patchJobError(ctx context.Context, jobID, msg string) error {
	body := map[string]any{
		"status": "error",
		"error":  msg,
	}
	return c.doJSON(ctx, "PATCH", "/api/v1/admin/backfill/jobs/"+url.PathEscape(jobID), body, nil)
}

// doJSON is the shared JSON POST/PATCH/GET helper.
func (c *apiClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// boomtime uses Wakatime-compatible Basic auth (see internal/auth/auth.go
	// ParseAuthHeader): server strips "Basic " prefix, hashes the rest, and
	// looks up the hashed value in auth_tokens.hashed_token. The stored hash
	// was computed over base64(raw-uuid) at CreateAPIToken time, so the wire
	// value must be base64(uuid) — NOT the raw uuid, NOT "Bearer <uuid>".
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.token)))
	// Real UA — Cloudflare's default bot-management drops the stock
	// "Go-http-client/1.1" with a 403 challenge page. Anything self-identifying
	// and non-headless-looking passes.
	req.Header.Set("User-Agent", "boomtime-backfill-cli/1.0 (+https://github.com/TheBranchDriftCatalyst/boomtime)")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, string(b))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
