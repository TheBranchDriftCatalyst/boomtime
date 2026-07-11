package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/labstack/echo/v5"
)

const defaultNumOfCommits int64 = 40

// Commits: GET /api/v1/commits/:project/report?repoName&repoOwner&user&limit.
func (h *Handler) Commits(c *echo.Context) error {
	_, username, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	project := c.Param("project")
	repoName := c.QueryParam("repoName")
	repoOwner := c.QueryParam("repoOwner")
	user := c.QueryParam("user")

	if repoName == "" {
		return respondErr(c, apierr.MissingQueryParam("repoName"))
	}
	if repoOwner == "" {
		return respondErr(c, apierr.MissingQueryParam("repoOwner"))
	}
	if user == "" {
		return respondErr(c, apierr.MissingQueryParam("user"))
	}

	if h.Cfg.GithubToken == "" {
		return respondErr(c, apierr.MissingGithubToken())
	}

	numCommits := queryInt64(c, "limit", defaultNumOfCommits)

	// Fetch one extra commit: the last commit's time cannot be computed.
	repoCommits, err := h.fetchCommits(repoOwner, repoName, numCommits+1)
	if err != nil {
		h.Logger.Warn("github commit fetch failed", "err", err)
		msg := "HTTP call to api.github.com failed"
		return respondErr(c, apierr.GenericHTTP(msg, nil))
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
	var users, projects []string
	var mins, maxs []time.Time
	for i := 1; i < len(usersCommits); i++ {
		a := usersCommits[i]   // tail
		b := usersCommits[i-1] // init
		users = append(users, username)
		projects = append(projects, project)
		mins = append(mins, a.Commit.Author.Date)
		maxs = append(maxs, b.Commit.Author.Date)
	}

	var timeSpent []int64
	if len(users) > 0 {
		timeSpent, err = h.DB.GetTotalTimeBetween(ctx, users, projects, mins, maxs)
		if err != nil {
			return respondErr(c, apierr.Generic())
		}
	}

	// Map sha -> commit with total_seconds set (zip commitGaps with timeSpent).
	withTime := map[string]model.CommitPayload{}
	for i := 1; i < len(usersCommits) && i-1 < len(timeSpent); i++ {
		b := usersCommits[i-1] // init element in the gap
		secs := timeSpent[i-1]
		b.TotalSeconds = &secs
		withTime[b.Sha] = b
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

	return c.JSON(http.StatusOK, model.CommitReport{Commits: result})
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
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(h.Cfg.GithubToken)))
	req.Header.Set("User-Agent", "Hakatime Server")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var commits []model.CommitPayload
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return nil, err
	}
	return commits, nil
}
