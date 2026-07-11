// Leaderboards types mirroring the Go backend JSON payloads.

export interface LeaderboardEntry {
  name: string;
  value: number;
}

// Normalized shape returned by api.getLeaderboards(). Backend emits hakatime's
// raw { global, lang }; api.ts maps the per-language key `lang` -> `languages`.
export interface LeaderboardsPayload {
  global: LeaderboardEntry[];
  languages: Record<string, LeaderboardEntry[]>;
}
