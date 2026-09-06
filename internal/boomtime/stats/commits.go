package stats

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/labstack/echo/v5"
)

const defaultNumOfCommits int64 = 40

// Commits: GET /api/v1/commits/:project/report?repoName&repoOwner&user&limit.
func (h *Handler) Commits(c *echo.Context) (model.CommitReport, error) {
	var out model.CommitReport
	username, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	project := c.Param("project")
	repoName := c.QueryParam("repoName")
	repoOwner := c.QueryParam("repoOwner")
	user := c.QueryParam("user")

	if repoName == "" {
		return out, apierr.MissingQueryParam("repoName")
	}
	if repoOwner == "" {
		return out, apierr.MissingQueryParam("repoOwner")
	}
	if user == "" {
		return out, apierr.MissingQueryParam("user")
	}

	if h.Cfg.GithubTokenValue() == "" {
		return out, apierr.MissingGithubToken()
	}

	numCommits := apihelpers.QueryInt64(c, "limit", defaultNumOfCommits)

	// Fetch one extra commit: the last commit's time cannot be computed.
	repoCommits, err := h.fetchCommits(repoOwner, repoName, numCommits+1)
	if err != nil {
		// Kept explicit: GenericHTTP is an *apierr.Error, so the seam renders it
		// without logging — dropping this Warn would lose the upstream cause.
		h.Logger.Warn("github commit fetch failed", "err", err)
		msg := "HTTP call to api.github.com failed"
		return out, apierr.GenericHTTP(msg, nil)
	}

	// Filter to the user's non-merge commits.
	var usersCommits []model.CommitPayload
	for _, cm := range repoCommits {
		if cm.Author.Login == user && len(cm.Parents) <= 1 {
			usersCommits = append(usersCommits, cm)
		}
	}

	// Build the time ranges between consecutive commits (author dates).
	ctx := c.Request().Context()
	users, projects, mins, maxs := commitGapWindows(username, project, usersCommits)

	var timeSpent []int64
	if len(users) > 0 {
		timeSpent, err = h.DB.GetTotalTimeBetween(ctx, users, projects, mins, maxs)
		if err != nil {
			return out, fmt.Errorf("commit time aggregation failed: %w", err)
		}
	}

	// Map sha -> commit with total_seconds set.
	withTime, err := attributeCommitSeconds(usersCommits, timeSpent)
	if err != nil {
		return out, fmt.Errorf("commit time attribution failed: %w", err)
	}

	// Update repoCommits with computed times and take the requested count.
	result := make([]model.CommitPayload, 0, len(repoCommits))
	for _, cm := range repoCommits {
		if v, ok := withTime[cm.Sha]; ok {
			result = append(result, v)
		} else {
			result = append(result, cm)
		}
	}
	if int64(len(result)) > numCommits {
		result = result[:numCommits]
	}

	return model.CommitReport{Commits: result}, nil
}

// commitGapWindows turns a newest-first commit list into the parallel
// (user, project, min, max) arrays GetTotalTimeBetween consumes. Window j
// spans [author date of commits[j+1], author date of commits[j]] — the work
// that produced commits[j] — so window j belongs to commits[j]. The N-1
// windows are emitted in the SAME order as the commit list (newest first);
// attributeCommitSeconds relies on that.
func commitGapWindows(username, project string, commits []model.CommitPayload) (users, projects []string, mins, maxs []time.Time) {
	for i := 1; i < len(commits); i++ {
		a := commits[i]   // tail (older bound)
		b := commits[i-1] // init (newer bound; the commit that closes the window)
		users = append(users, username)
		projects = append(projects, project)
		mins = append(mins, a.Commit.Author.Date)
		maxs = append(maxs, b.Commit.Author.Date)
	}
	return users, projects, mins, maxs
}

// attributeCommitSeconds zips per-window totals back onto the commit that
// closes each window, returning sha -> commit with TotalSeconds set.
//
// boom-gsnv: the zip is positional, and that is only sound because
// GetTotalTimeBetween now guarantees one total per input window in input
// order (WITH ORDINALITY + LEFT JOIN + ORDER BY ordinality). Before that fix
// the DB dropped empty windows and returned an arbitrarily ordered slice that
// the DB layer then REVERSED, so every commit after the first heartbeat-free
// gap inherited a neighbour's seconds and the tail silently got none.
//
// The length mismatch is an error rather than a silent truncation for the
// same reason: a short slice is exactly the shape the old bug had, and
// labelling commits with someone else's coding time is worse than 500ing.
func attributeCommitSeconds(commits []model.CommitPayload, timeSpent []int64) (map[string]model.CommitPayload, error) {
	want := 0
	if len(commits) > 1 {
		want = len(commits) - 1
	}
	if len(timeSpent) != want {
		return nil, fmt.Errorf("got %d window totals for %d commit gaps", len(timeSpent), want)
	}
	withTime := make(map[string]model.CommitPayload, want)
	for j, secs := range timeSpent {
		b := commits[j] // the commit that closes window j
		secs := secs    // per-iteration copy: TotalSeconds is a *int64
		b.TotalSeconds = &secs
		withTime[b.Sha] = b
	}
	return withTime, nil
}

// fetchCommits queries the GitHub commits API for a repo.
func (h *Handler) fetchCommits(owner, name string, perPage int64) ([]model.CommitPayload, error) {
	u := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) +
		"/commits?per_page=" + strconv.FormatInt(perPage, 10)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// hakatime sends Basic <token>; keep parity.
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(h.Cfg.GithubTokenValue())))
	req.Header.Set("User-Agent", "Hakatime Server")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned status %d for %s/%s", resp.StatusCode, owner, name)
	}

	var commits []model.CommitPayload
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return nil, err
	}
	return commits, nil
}
