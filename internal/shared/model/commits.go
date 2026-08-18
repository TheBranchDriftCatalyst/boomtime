package model

import "time"

// ---- Commits (Commits.hs) ----

// CommitReport is {"commits": [...]}.
type CommitReport struct {
	Commits []CommitPayload `json:"commits"`
}

// AuthorData / CommitterData / CommitData mirror the GitHub commit shape with
// noPrefixOptions applied to the wrapper structs.
type AuthorData struct {
	Name  string    `json:"name"`  // authorName
	Email string    `json:"email"` // authorEmail
	Date  time.Time `json:"date"`  // authorDate
}

type CommitterData struct {
	Name  string    `json:"name"`  // committerName
	Email string    `json:"email"` // committerEmail
	Date  time.Time `json:"date"`  // committerDate
}

type CommitData struct {
	URL       string        `json:"url"`       // dataUrl
	Author    AuthorData    `json:"author"`    // dataAuthor
	Committer CommitterData `json:"committer"` // dataCommitter
	Message   string        `json:"message"`   // dataMessage
}

type AuthorPayload struct {
	Login string `json:"login"` // authorLogin
}

type CommitParent struct {
	URL string `json:"url"` // cmUrl
	Sha string `json:"sha"` // cmSha
}

type CommitPayload struct {
	URL          string         `json:"url"`           // pUrl
	Sha          string         `json:"sha"`           // pSha
	HTMLURL      string         `json:"html_url"`      // pHtml_url -> html_url
	Commit       CommitData     `json:"commit"`        // pCommit
	Author       AuthorPayload  `json:"author"`        // pAuthor
	Parents      []CommitParent `json:"parents"`       // pParents
	TotalSeconds *int64         `json:"total_seconds"` // pTotal_seconds -> total_seconds
}
