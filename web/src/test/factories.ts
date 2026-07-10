// Payload factories producing realistic RAW hakatime/backend shapes, so the
// api.ts normalizer tests exercise the real key remapping (tknId→id,
// timelineLangs→langs, lang→languages, etc.). Every factory allows overrides.

// --- Auth --------------------------------------------------------------------

export function authResponse(over: Partial<{
  token: string;
  tokenExpiry: string;
  tokenUsername: string;
}> = {}) {
  return {
    token: "YWJjZDEyMzQ=", // base64(uuid) — sent verbatim in the header
    tokenExpiry: "2026-07-10T12:00:00.000Z",
    tokenUsername: "panda",
    ...over,
  };
}

// Raw StoredApiToken shape (noPrefix source keys tknId/tknName/tknDesc).
export function rawToken(over: Partial<{
  tknId: string;
  tknName: string | null;
  tknDesc: string | null;
  lastUsage: string | null;
}> = {}) {
  return {
    tknId: "tok-1",
    tknName: "laptop",
    tknDesc: null,
    lastUsage: "2026-07-01T00:00:00.000Z",
    ...over,
  };
}

// --- Timeline (raw timelineLangs) --------------------------------------------

export function rawTimeline(
  langs: Record<
    string,
    Array<{ tName: string; tRangeStart: string; tRangeEnd: string }>
  > = {
    TypeScript: [
      {
        tName: "TypeScript",
        tRangeStart: "2026-07-09T10:00:00Z",
        tRangeEnd: "2026-07-09T11:00:00Z",
      },
    ],
  },
) {
  return { timelineLangs: langs };
}

// --- Leaderboards (raw lang key) ---------------------------------------------

export function rawLeaderboards(
  over: Partial<{
    global: Array<{ name: string; value: number }>;
    lang: Record<string, Array<{ name: string; value: number }>>;
  }> = {},
) {
  return {
    global: [{ name: "panda", value: 3600 }],
    lang: { TypeScript: [{ name: "panda", value: 1800 }] },
    ...over,
  };
}

// --- Curation ----------------------------------------------------------------

export function curationRule(over: Partial<{
  id: number;
  axis: string;
  action: "hide" | "rename";
  matchValue: string;
  newValue: string | null;
  matchType: "exact" | "regex";
  createdAt: string;
}> = {}) {
  return {
    id: 1,
    axis: "project",
    action: "rename" as const,
    matchValue: "gaka",
    newValue: "gakatime",
    matchType: "exact" as const,
    createdAt: "2026-07-01T00:00:00.000Z",
    ...over,
  };
}

export function curationRules(rules = [curationRule()]) {
  return { rules };
}

// --- Stats (only the fields the FE reads) ------------------------------------

export function statsPayload(over: Record<string, unknown> = {}) {
  return {
    startDate: "2026-06-25T00:00:00Z",
    endDate: "2026-07-10T00:00:00Z",
    totalSeconds: 52320,
    dailyAvg: 3600,
    dailyTotal: [0, 3600, 0, 7200],
    projects: [{ name: "gakatime", totalSeconds: 40000, totalPct: 0.8, totalDaily: [], pctDaily: [] }],
    languages: [{ name: "TypeScript", totalSeconds: 30000, totalPct: 0.6, totalDaily: [], pctDaily: [] }],
    platforms: [],
    machines: [],
    editors: [],
    projectsCount: 3,
    languagesCount: 5,
    platformsCount: 1,
    machinesCount: 1,
    editorsCount: 2,
    ...over,
  };
}

// --- Import ------------------------------------------------------------------

export function importJob(over: Record<string, unknown> = {}) {
  return {
    id: 1,
    owner: "panda",
    state: "running",
    startDate: "2026-06-01",
    endDate: "2026-07-01",
    totalDays: 30,
    processedDays: 10,
    importedCount: 500,
    currentDay: "2026-06-10",
    error: null,
    createdAt: "2026-07-01T00:00:00Z",
    startedAt: "2026-07-01T00:00:01Z",
    finishedAt: null,
    ...over,
  };
}

export function importLog(over: Record<string, unknown> = {}) {
  return {
    id: 1,
    ts: "2026-07-01T00:00:02Z",
    level: "info",
    message: "Fetched day 2026-06-10",
    ...over,
  };
}

// --- Heartbeats group --------------------------------------------------------

export function groupPayload(over: Record<string, unknown> = {}) {
  return {
    groupBy: "project",
    groups: [
      { value: "gakatime", count: 100, seconds: 3600, firstSeen: "2026-06-01T00:00:00Z", lastSeen: "2026-07-01T00:00:00Z" },
      { value: null, count: 5, seconds: 60, firstSeen: "2026-06-01T00:00:00Z", lastSeen: "2026-06-02T00:00:00Z" },
    ],
    truncated: false,
    ...over,
  };
}
