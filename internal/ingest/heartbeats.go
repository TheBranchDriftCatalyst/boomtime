package ingest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/wakatime"
	"github.com/labstack/echo/v5"
)

// Heartbeat ingests a single heartbeat: POST /api/v1/users/current/heartbeats.
// Body is capped at apihelpers.BodyLimitLarge (8 MiB) — a single heartbeat is ~500 bytes so
// the cap is generous, but bounded so an authenticated hostile client can't OOM
// the process with a multi-GB body (gaka-d6x.handler critique fix).
func (h *Handler) Heartbeat(c *echo.Context) error {
	var hb model.HeartbeatPayload
	if aerr := apihelpers.BindJSONWithLimit(c, &hb, apihelpers.BodyLimitLarge); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	return h.storeAndRespond(c, []model.HeartbeatPayload{hb})
}

// HeartbeatBulk ingests many heartbeats: POST /api/v1/users/current/heartbeats.bulk.
// Body is capped at apihelpers.BodyLimitLarge (8 MiB) — enough for ~10K-20K batched
// heartbeats but bounded to prevent DoS via oversized ingest (gaka-d6x.handler).
func (h *Handler) HeartbeatBulk(c *echo.Context) error {
	var hbs []model.HeartbeatPayload
	if aerr := apihelpers.BindJSONWithLimit(c, &hbs, apihelpers.BodyLimitLarge); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	return h.storeAndRespond(c, hbs)
}

func (h *Handler) storeAndRespond(c *echo.Context, hbs []model.HeartbeatPayload) error {
	_, owner, aerr := apihelpers.ResolveUser(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()

	machine := headerPtr(c, "X-Machine-Name")

	// Optional remote-write (best effort, ignore errors) before enrichment.
	if h.Cfg.RemoteWrite != nil {
		go h.remoteWrite(hbs, machine)
	}

	// Enrich: user-agent parse, sender, machine, language detection, empty-project fix.
	enriched := make([]model.HeartbeatPayload, len(hbs))
	for i, hb := range hbs {
		info := wakatime.UserAgentInfo(hb.UserAgent)
		hb.Sender = &owner
		hb.Editor = info.Editor
		hb.Plugin = info.Plugin
		hb.Platform = info.Platform
		hb.Machine = machine
		if hb.Language == nil && hb.Type == model.FileType {
			hb.Language = wakatime.LanguageFromEntity(hb.Entity)
		}
		if hb.Project != nil && *hb.Project == "" {
			unknown := "Unknown project"
			hb.Project = &unknown
		}
		enriched[i] = hb
	}

	ids, err := h.DB.SaveHeartbeats(ctx, enriched)
	if err != nil {
		h.Logger.Error("failed to store heartbeats", "err", err)
		return apihelpers.RespondErr(c, apierr.Generic())
	}

	// gaka-wpb: new activity might have flipped a goal — clear the cache
	// so the next GET /goals/:id/progress recomputes under the fresh
	// data. Best-effort (non-fatal): a failure here shouldn't sink the
	// ingest response. The eager invalidation complements the 60s TTL
	// stale-while-revalidate policy so freshly ingested data isn't
	// hidden for up to a minute.
	if err := goals.InvalidateGoalsForOwner(h.DB, ctx, owner); err != nil {
		h.Logger.Warn("goal cache invalidation failed after ingest (non-fatal)", "owner", owner, "err", err)
	}

	// Build the nested {"responses": [[{"data":{"id":"<id>"}}, 201], ...]} envelope.
	responses := make([][]any, len(ids))
	for i, id := range ids {
		responses[i] = []any{
			model.HeartbeatData{Data: model.HeartbeatID{ID: strconv.FormatInt(id, 10)}},
			201,
		}
	}
	return c.JSON(http.StatusAccepted, model.BulkHeartbeatData{Responses: responses})
}

func headerPtr(c *echo.Context, name string) *string {
	v := c.Request().Header.Get(name)
	if v == "" {
		return nil
	}
	return &v
}

// remoteWrite forwards heartbeats to a Wakatime-compatible endpoint.
func (h *Handler) remoteWrite(hbs []model.HeartbeatPayload, machine *string) {
	body, err := json.Marshal(hbs)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, h.Cfg.RemoteWrite.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	enc := base64.StdEncoding.EncodeToString([]byte(h.Cfg.RemoteWrite.Token))
	req.Header.Set("Authorization", "Basic "+enc)
	req.Header.Set("Content-Type", "application/json")
	if machine != nil {
		req.Header.Set("X-Machine-Name", *machine)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		h.Logger.Debug("remote write failed", "err", err)
		return
	}
	resp.Body.Close()
}
