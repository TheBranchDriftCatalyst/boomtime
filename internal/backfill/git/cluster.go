// cluster.go: turn a bag of commits into a list of coding sessions.
//
// Rules:
//   - Sort commits ascending by time (Scanner yields newest-first).
//   - Walk sorted commits; open a new session whenever the gap from the
//     previous commit exceeds ClusterGap.
//   - Session start = first commit time − PreCommitLead (mirrors "you
//     typed for a while before hitting save/commit").
//   - Session end   = last  commit time + PostCommitTail.
//   - Language = language derived from the file with the most touched
//     lines across the whole session (falls back to "" for binary /
//     unknown extensions).
//   - TopFile = the file with the most touched lines. Used as `entity`
//     for every heartbeat in the session so time attributes to that
//     path.
//
// Cluster is pure: given the same input slice + config it produces the
// same []Session output. Materialize below is the same story for
// []Heartbeat generation. Both are trivially unit-testable without any
// git repo state.

package git

import (
	"sort"
	"time"
)

// Cluster groups commits into sessions using cfg.ClusterGap. Commits do
// not have to be pre-sorted; Cluster copies + sorts internally so the
// caller can pass a straight iteration output.
//
// Empty input returns nil. All-filtered-out input (Scanner yielded 0)
// also returns nil.
func Cluster(commits []Commit, cfg EstimatorConfig) []Session {
	if len(commits) == 0 {
		return nil
	}
	cfg = cfg.defaults()

	// Sort ascending (oldest → newest). Stable sort so commits with
	// identical timestamps preserve input order (deterministic clusters).
	sorted := make([]Commit, len(commits))
	copy(sorted, commits)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Time.Before(sorted[j].Time)
	})

	var sessions []Session
	current := []Commit{sorted[0]}
	flush := func() {
		if len(current) == 0 {
			return
		}
		s := buildSession(current, cfg)
		sessions = append(sessions, s)
	}

	for i := 1; i < len(sorted); i++ {
		gap := sorted[i].Time.Sub(sorted[i-1].Time)
		if gap > cfg.ClusterGap {
			flush()
			current = []Commit{sorted[i]}
			continue
		}
		current = append(current, sorted[i])
	}
	flush()
	return sessions
}

// buildSession pads the [first, last] commit range with lead/tail,
// computes the top file (by lines touched across every commit) and the
// derived language. A session with commits but no file-level diff data
// (very rare — merge commits are already dropped upstream) yields Session
// with empty TopFile and Language.
func buildSession(commits []Commit, cfg EstimatorConfig) Session {
	// Guaranteed non-empty by caller.
	first := commits[0].Time
	last := commits[len(commits)-1].Time
	s := Session{
		RepoName: commits[0].RepoName,
		Start:    first.Add(-cfg.PreCommitLead),
		End:      last.Add(cfg.PostCommitTail),
		Commits:  commits,
	}

	// Tally lines touched per file across the whole session. The file
	// with the biggest churn wins as TopFile and its extension picks the
	// language.
	touchedPerFile := map[string]int{}
	for _, c := range commits {
		// Distribute this commit's total lines-changed evenly across
		// its files. We don't have per-file line counts by design (see
		// scanner.diffStatsAgainstFirstParent — it sums), so an even
		// split is the least-biased approximation.
		total := c.LinesAdded + c.LinesDeleted
		if len(c.FilesChanged) == 0 || total == 0 {
			// Fallback: still count each file once so an all-rename /
			// permissions-only commit contributes something.
			for _, f := range c.FilesChanged {
				touchedPerFile[f] += 1
			}
			continue
		}
		perFile := total / len(c.FilesChanged)
		if perFile == 0 {
			perFile = 1
		}
		for _, f := range c.FilesChanged {
			touchedPerFile[f] += perFile
		}
	}

	// Pick the max.
	var topFile string
	var topScore int
	for f, score := range touchedPerFile {
		if score > topScore || (score == topScore && f < topFile) {
			topFile = f
			topScore = score
		}
	}
	s.TopFile = topFile
	if topFile != "" {
		s.Language = cfg.languageFor(topFile)
	}
	// Store the weights + per-file language map so Materialize can
	// distribute heartbeats across every file touched, not just TopFile.
	if len(touchedPerFile) > 0 {
		s.FileWeights = touchedPerFile
		s.FileLanguages = make(map[string]string, len(touchedPerFile))
		for f := range touchedPerFile {
			if lang := cfg.languageFor(f); lang != "" {
				s.FileLanguages[f] = lang
			}
		}
	}
	return s
}

// Materialize emits synthetic heartbeats spaced HeartbeatRate apart
// across [session.Start, session.End]. The first heartbeat lands at
// session.Start, the last is the largest step that still ≤ session.End.
//
// Every heartbeat carries:
//   - entity   = session.TopFile (empty → "backfill:<repo>" placeholder
//                so the DB constraint entity NOT NULL still holds)
//   - project  = session.RepoName
//   - language = session.Language (may be empty)
//   - sender   = sourceTag ("backfill:git" typically)
//   - category = "coding"
//   - type     = "file"
//   - time     = the current step's unix seconds (float, hakatime shape)
//
// The `entity` fallback matters: a session with zero file-level diff
// data (all merges filtered out but the walk still saw a root commit
// with no listable files) would otherwise produce empty-entity
// heartbeats which the heartbeats.entity NOT NULL constraint rejects.
// The placeholder is unique per repo so it can't collide with a real
// filename.
func Materialize(sess Session, sourceTag string, rate time.Duration) []Heartbeat {
	if rate <= 0 {
		rate = 2 * time.Minute
	}
	if sess.End.Before(sess.Start) {
		return nil
	}
	// Pre-compute the number of heartbeats + the file pattern (one entity
	// per slot, weighted by how many lines the file received in this
	// session). Falls back to TopFile / placeholder when FileWeights is
	// unset (e.g. manually-constructed Session in tests).
	timestamps := timestampSteps(sess.Start, sess.End, rate)
	pattern := buildFilePattern(sess, len(timestamps))

	project := sess.RepoName
	sender := sourceTag
	// sess.Language is the SESSION-level dominant language, used only
	// when FileLanguages is empty (fallback path).
	var defaultLang *string
	if sess.Language != "" {
		lang := sess.Language
		defaultLang = &lang
	}
	ua := "boomtime-backfill-git/1"

	hbs := make([]Heartbeat, 0, len(timestamps))
	for i, t := range timestamps {
		entity := pattern[i]
		// Per-file language when we have it (from Cluster's derivation);
		// fall back to the session-level language.
		var language *string
		if l, ok := sess.FileLanguages[entity]; ok && l != "" {
			ll := l
			language = &ll
		} else {
			language = defaultLang
		}
		p := project
		s := sender
		hb := Heartbeat{
			Entity:    entity,
			Type:      "file",
			Category:  "coding",
			Time:      float64(t.UnixNano()) / 1e9,
			Project:   &p,
			Language:  language,
			Sender:    &s,
			UserAgent: ua,
		}
		hbs = append(hbs, hb)
	}
	return hbs
}

// timestampSteps yields the sequence of heartbeat instants from start
// through end, stepping by `rate`. The first tick is start; the last is
// the largest step ≤ end (matches the original range-loop semantics).
func timestampSteps(start, end time.Time, rate time.Duration) []time.Time {
	if rate <= 0 || end.Before(start) {
		return nil
	}
	out := make([]time.Time, 0, int(end.Sub(start)/rate)+1)
	for t := start; !t.After(end); t = t.Add(rate) {
		out = append(out, t)
	}
	return out
}

// buildFilePattern returns a slice of length `n` where each slot holds
// the file that should back that heartbeat, proportional to the file's
// weight in sess.FileWeights.
//
// The distribution uses largest-remainder allocation (Hamilton method):
// slot i picks the file with the biggest deficit between its expected
// cumulative allocation ((i+1)*weight/total) and its actual allocated
// count. Result: an interleaved pattern that hits every file the
// operator touched, in proportion to how much they touched it — instead
// of the pre-fix behavior of all N slots getting TopFile.
//
// Fallback path (empty weights): returns n slots of TopFile, or a
// placeholder "backfill:<repo>" when TopFile is empty too. Preserves
// existing behavior for manually-constructed Session values.
func buildFilePattern(sess Session, n int) []string {
	if n <= 0 {
		return nil
	}
	// Fallback: no per-file weights → repeat TopFile / placeholder.
	if len(sess.FileWeights) == 0 {
		entity := sess.TopFile
		if entity == "" {
			entity = "backfill:" + sess.RepoName
		}
		out := make([]string, n)
		for i := range out {
			out[i] = entity
		}
		return out
	}
	// Sort files deterministically (weight desc, then name asc) so the
	// pattern is stable across runs — testable + reproducible.
	type fw struct {
		file   string
		weight int
	}
	files := make([]fw, 0, len(sess.FileWeights))
	total := 0
	for f, w := range sess.FileWeights {
		if w > 0 {
			files = append(files, fw{f, w})
			total += w
		}
	}
	if len(files) == 0 || total == 0 {
		// Weights all zero for some reason — fall back like empty path.
		return buildFilePattern(Session{RepoName: sess.RepoName, TopFile: sess.TopFile}, n)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].weight != files[j].weight {
			return files[i].weight > files[j].weight
		}
		return files[i].file < files[j].file
	})
	allocated := make(map[string]int, len(files))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		// Pick the file with the biggest deficit vs its expected share.
		var bestFile string
		var bestDeficit float64
		first := true
		for _, f := range files {
			expected := float64(i+1) * float64(f.weight) / float64(total)
			deficit := expected - float64(allocated[f.file])
			if first || deficit > bestDeficit {
				bestFile = f.file
				bestDeficit = deficit
				first = false
			}
		}
		out = append(out, bestFile)
		allocated[bestFile]++
	}
	return out
}
