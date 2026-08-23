// Auth / account types mirroring the Go backend JSON payloads.

export interface AuthResponse {
  token: string;
  tokenExpiry: string; // ISO timestamp
  tokenUsername: string;
}

export interface Credentials {
  username: string;
  password: string;
}

export interface CreateTokenResponse {
  apiToken: string;
}

// Normalized shape returned by api.getTokens(). The backend emits hakatime's
// raw StoredApiToken (default aeson: tknId/tknName/tknDesc/lastUsage); api.ts
// maps it to these ergonomic keys. `id` is the base64(uuid) token id.
export interface StoredApiToken {
  id: string;
  lastUsage: string | null;
  name: string | null;
  desc: string | null;
}

export interface CurrentUser {
  data: {
    full_name: string | null;
    email: string | null;
    photo: string | null;
    // boom-myv: BOOM_ADMIN_USERS membership. Drives the Settings > Admin tab
    // visibility. Missing when the server hasn't been updated — treat as
    // false in that case.
    is_admin?: boolean;
    // boom-dg7: user's raw stored IANA name ('' = never picked). Optional
    // because older server binaries don't emit it — the auto-detect logic
    // treats missing as "not picked" so nothing gets falsely auto-detected
    // against an unknown server.
    timezone?: string;
    // boom-dg7: what the server ACTUALLY resolves to via the 3-level chain.
    // NEVER "" when present. The Settings picker uses the difference between
    // this and `timezone` to render "your choice" vs "server default", and
    // the first-login auto-detect only fires when
    //   timezone === '' && browserTZ !== effective_timezone
    effective_timezone?: string;
  };
}
