// books_annotations.go — the annotation read surface (boom-siwi.5).
//
// GET /api/v1/books/items/:externalId/annotations returns one book's highlights,
// notes and (from phase 2) clips, in reading order.
//
// The DTO deliberately exposes body and transcript as SEPARATE fields rather
// than collapsing them into one "text". They have different provenance — body is
// what Amazon says the user highlighted, transcript is what a speech model
// guessed from a clip we cut ourselves — and a consumer that cannot tell them
// apart cannot weight them differently or show the user which is which.
package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// bookAnnotationDTO is one highlight, note, bookmark or clip.
type bookAnnotationDTO struct {
	// Kind is highlight | note | bookmark | clip.
	Kind string `json:"kind"`
	// Source is the ingest that produced it: kindle | audible.
	Source string `json:"source"`

	// PositionUnit names the units PositionStart/End are expressed in:
	// location or page for Kindle, millis for an Audible clip. It is stored per
	// row rather than inferred from source, because Kindle reports a location for
	// a reflowable book and a page for a print replica.
	PositionUnit string `json:"positionUnit"`
	// PositionStart is where the annotation begins, in PositionUnit.
	PositionStart int64 `json:"positionStart"`
	// PositionEnd is where it ends, omitted for point annotations (a note or a
	// bookmark has no range).
	PositionEnd *int64 `json:"positionEnd,omitempty"`

	// Body is the passage the user highlighted, as Amazon reported it. Empty for
	// a standalone note or an untranscribed clip.
	Body string `json:"body,omitempty"`
	// Note is the user's own margin note.
	Note string `json:"note,omitempty"`

	// Transcript is text WE derived — a clip cut from the liberated audio and run
	// through a speech model. Never Amazon's words.
	Transcript string `json:"transcript,omitempty"`
	// TranscriptSource names the model that produced Transcript, e.g.
	// "whisper:large-v3". Empty when there is no transcript.
	TranscriptSource string `json:"transcriptSource,omitempty"`

	// CapturedAt is when the annotation was MADE, per the source, RFC3339.
	// Omitted when the source does not report one — the Kindle notebook does not,
	// and dating a highlight to the moment we first scraped it would be a
	// fabrication.
	CapturedAt string `json:"capturedAt,omitempty"`
}

// bookAnnotationsResponse is the GET payload.
type bookAnnotationsResponse struct {
	// Annotations are ordered by position — reading order within the book.
	Annotations []bookAnnotationDTO `json:"annotations"`
	// Counts is the number of live annotations per kind, so a caller can render a
	// summary without walking the list.
	Counts map[string]int `json:"counts"`
}

// GetBookAnnotations: GET /api/v1/books/items/:externalId/annotations
// Returns the caller's annotations for one book, oldest position first.
// ?source= (kindle|audible) narrows to one source; omitted returns both.
func (h *Handler) GetBookAnnotations(c *echo.Context) (bookAnnotationsResponse, error) {
	out := bookAnnotationsResponse{Annotations: []bookAnnotationDTO{}, Counts: map[string]int{}}
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	asin := strings.TrimSpace(c.Param("externalId"))
	if asin == "" {
		return out, apierr.BadRequest("missing book id")
	}
	source := strings.TrimSpace(c.QueryParam("source"))
	if source != "" && source != "kindle" && source != "audible" {
		return out, apierr.New(http.StatusBadRequest, "source must be kindle or audible", nil)
	}

	ctx := c.Request().Context()
	rows, err := h.DB.ListBookAnnotations(ctx, owner, source, asin)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Annotations = append(out.Annotations, toBookAnnotationDTO(r))
		out.Counts[r.Kind]++
	}
	return out, nil
}

func toBookAnnotationDTO(a db.BookAnnotation) bookAnnotationDTO {
	dto := bookAnnotationDTO{
		Kind:             a.Kind,
		Source:           a.Source,
		PositionUnit:     a.PositionUnit,
		PositionStart:    a.PositionStart,
		PositionEnd:      a.PositionEnd,
		Body:             a.Body,
		Note:             a.Note,
		Transcript:       a.Transcript,
		TranscriptSource: a.TranscriptSource,
	}
	if a.CapturedAt != nil {
		dto.CapturedAt = a.CapturedAt.UTC().Format(time.RFC3339)
	}
	return dto
}
