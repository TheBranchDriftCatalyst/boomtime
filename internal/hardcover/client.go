package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// Endpoint is the Hardcover GraphQL API (Hasura). Every response is HTTP 200
// even on error, so callers MUST inspect the body-level errors[] — see graphql.
const Endpoint = "https://api.hardcover.app/v1/graphql"

// Sentinel errors the JOB layer keys off of (same contract as internal/github).
var (
	// ErrBadToken means Hardcover rejected the bearer token (HTTP 401 or an
	// auth-message in errors[]). The caller flips hardcover_key_status=invalid
	// and prompts a re-paste (the Jan-1 reset makes this routine).
	ErrBadToken = errors.New("hardcover: bad or expired token")
	// ErrRateLimited means Hardcover throttled us (HTTP 429 or a rate-limit
	// message in errors[]). The caller returns it so the job retries with
	// backoff.
	ErrRateLimited = errors.New("hardcover: rate limited")
)

// --- Dry-run safety gate ----------------------------------------------------
// Every Hardcover MUTATION (write) is gated behind a process-wide dry-run flag.
// When on (the FAIL-SAFE DEFAULT), no write ever reaches Hardcover — the intended
// operation is logged instead ("hardcover DRYRUN: would <op>") and the call
// returns success. READS (me/editions/search/library) always pass through. This
// protects a user's real Hardcover library while the sync mechanism is built.
// Toggle via BOOM_HARDCOVER_DRYRUN (default true); wire through hardcover.Configure.
var (
	defaultDryRun = true // fail-safe: writes blocked unless explicitly enabled
	defaultLogger *slog.Logger
)

// Configure sets the process-wide dry-run default + logger for clients built by
// NewClient. Call once at startup. dryRun=true blocks+logs all mutations.
func Configure(dryRun bool, logger *slog.Logger) {
	defaultDryRun = dryRun
	defaultLogger = logger
}

// isMutation reports whether a GraphQL document is a mutation (a write).
func isMutation(query string) bool {
	return strings.HasPrefix(strings.TrimSpace(query), "mutation")
}

// GraphQLError is one entry of a GraphQL response's errors[] array.
type GraphQLError struct {
	Message    string          `json:"message"`
	Extensions json.RawMessage `json:"extensions,omitempty"`
}

func (e GraphQLError) Error() string { return e.Message }

// Client is a thin, throttled GraphQL client bound to one user's bearer token.
// Throttle: < 60 req/min (Hardcover's documented ceiling) via a 1-req/second
// token bucket — the match ladder is the expensive part, which is why matched
// rows are cached and never re-fuzzed.
type Client struct {
	token   string
	http    *http.Client
	limiter *rate.Limiter
	dryRun  bool
	logger  *slog.Logger
}

// NewClient builds a client for a bearer token. The token is used only in the
// Authorization header and is never logged. It inherits the process-wide dry-run
// default (see Configure) so every write is blocked unless dry-run is disabled.
func NewClient(token string) *Client {
	return &Client{
		token:   strings.TrimSpace(token),
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Every(time.Second), 1),
		dryRun:  defaultDryRun,
		logger:  defaultLogger,
	}
}

// SetDryRun overrides the dry-run flag on this client (tests / explicit opt-in).
func (c *Client) SetDryRun(v bool) *Client { c.dryRun = v; return c }

// DryRun reports whether this client blocks writes.
func (c *Client) DryRun() bool { return c.dryRun }

// logBlockedMutation records an intended-but-blocked write. vars carry only book
// ids / status / dates (never the token), so they are safe to log verbatim.
func (c *Client) logBlockedMutation(query string, vars map[string]any) {
	op := "mutation"
	if i := strings.IndexAny(strings.TrimPrefix(strings.TrimSpace(query), "mutation "), "(&{ "); i > 0 {
		op = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "mutation ")[:i])
	}
	body, _ := json.Marshal(vars)
	l := c.logger
	if l == nil {
		l = slog.Default()
	}
	l.Warn("hardcover DRYRUN: write blocked", "op", op, "vars", string(body))
}

// graphql POSTs a query + variables and unmarshals the response `data` into out
// (a pointer). It classifies auth/rate-limit failures into ErrBadToken /
// ErrRateLimited and otherwise returns the first body-level GraphQL error. The
// throttle is applied BEFORE the request so bursts can never exceed the budget.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	if c.token == "" {
		return ErrBadToken
	}
	// Dry-run safety gate: block + log every mutation (write). Reads pass through.
	// Returns nil (a simulated success) so callers proceed without surfacing an
	// error; `out` stays zero-valued (e.g. a returned id of 0), which downstream
	// writes also gate on, so the whole push chain is a no-op that logs its intent.
	if c.dryRun && isMutation(query) {
		c.logBlockedMutation(query, vars)
		return nil
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}

	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	// Transport-level status wins before we even parse: 401 = dead token,
	// 429 = throttled. Hardcover mostly answers 200, but a gateway/CDN may not.
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrBadToken
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []GraphQLError  `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("hardcover: parse response (status %d): %w", resp.StatusCode, err)
	}
	if len(envelope.Errors) > 0 {
		return classifyGraphQLErrors(envelope.Errors)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hardcover: unexpected status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 {
		return errors.New("hardcover: empty data in response")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("hardcover: decode data: %w", err)
	}
	return nil
}

// classifyGraphQLErrors maps a body-level errors[] to a sentinel where possible
// (auth → ErrBadToken, rate-limit → ErrRateLimited) so the job layer reacts
// correctly; otherwise it returns the first error verbatim.
func classifyGraphQLErrors(errs []GraphQLError) error {
	for _, e := range errs {
		m := strings.ToLower(e.Message)
		switch {
		case strings.Contains(m, "unauthor") ||
			strings.Contains(m, "jwt") ||
			strings.Contains(m, "invalid token") ||
			strings.Contains(m, "not logged in") ||
			strings.Contains(m, "authentication"):
			return ErrBadToken
		case strings.Contains(m, "rate limit") || strings.Contains(m, "too many requests"):
			return ErrRateLimited
		}
	}
	return errs[0]
}

// Validate runs the `me{}` query to confirm the token is live and returns the
// Hardcover username (may be empty if the account exposes none). ErrBadToken on
// a rejected token. Used by the connect endpoint (validate-then-persist).
func (c *Client) Validate(ctx context.Context) (string, error) {
	// Hardcover's `me` resolves to a list of the authed user's records.
	const q = `query { me { id username } }`
	var data struct {
		Me []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"me"`
	}
	if err := c.graphql(ctx, q, nil, &data); err != nil {
		return "", err
	}
	if len(data.Me) == 0 {
		// A 200 with no auth error but an empty me{} still means the token is
		// authenticating SOMETHING; treat as valid with no username.
		return "", nil
	}
	return data.Me[0].Username, nil
}
