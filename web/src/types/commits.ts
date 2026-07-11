// Commit-report types mirroring the Go backend JSON payloads.

export interface Commit {
  html_url: string;
  total_seconds: number;
  commit: {
    message: string;
    author: { date: string };
  };
  author: { login: string };
}

export interface CommitReportPayload {
  commits: Commit[];
}
