import { http, HttpResponse } from "msw";
import {
  authResponse,
  curationRules,
  groupPayload,
  rawLeaderboards,
  rawTimeline,
  rawToken,
  statsPayload,
} from "@/test/factories";

// Default happy-path handlers returning realistic RAW backend shapes. Tests
// override individual routes with server.use(...) as needed.
export const handlers = [
  http.post("/auth/login", () => HttpResponse.json(authResponse())),
  http.post("/auth/register", () => HttpResponse.json(authResponse())),
  http.post("/auth/refresh_token", () => HttpResponse.json(authResponse())),
  http.post("/auth/logout", () => new HttpResponse(null, { status: 200 })),

  http.get("/auth/tokens", () => HttpResponse.json([rawToken()])),

  http.get("/api/v1/users/current/stats", () =>
    HttpResponse.json(statsPayload()),
  ),
  http.get("/api/v1/users/current/timeline", () =>
    HttpResponse.json(rawTimeline()),
  ),
  http.get("/api/v1/leaderboards", () =>
    HttpResponse.json(rawLeaderboards()),
  ),
  http.get("/api/v1/users/current/curation", () =>
    HttpResponse.json(curationRules()),
  ),
  http.get("/api/v1/users/current/heartbeats/group", () =>
    HttpResponse.json(groupPayload()),
  ),
  http.get("/api/v1/users/current/sources/health", () =>
    HttpResponse.json({ sources: [] }),
  ),
  http.get("/api/v1/users/current/spaces", () =>
    HttpResponse.json({ spaces: [] }),
  ),
  http.get("/api/v1/users/current/spaces/preview", () =>
    HttpResponse.json({ values: [], truncated: false }),
  ),
  // gaka-6jm.2: encrypted-at-rest Wakatime key presence probe. Default =
  // no saved key so the import form's fallback UX is exercised. Tests can
  // override with server.use to flip hasSavedKey.
  http.get("/api/v1/users/current/wakatime_key", () =>
    HttpResponse.json({ hasSavedKey: false }),
  ),
];

export { http, HttpResponse };
