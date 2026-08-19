// Books-standalone build flag (gaka-zp2s).
//
// `VITE_BOOKS_STANDALONE` is set to "true" ONLY by web/vite.books.config.ts via
// `define` — the host build never sets it. So in the host bundle this constant
// folds to `false` at build time and every `if (IS_BOOKS_STANDALONE)` branch
// dead-code-eliminates away: the host app stays byte-identical. The standalone
// books build folds it to `true`, activating the books-only shell gates (nav
// scope, branding, first-run onboarding, and the notify/jobs/spaces shell
// backend calls the lean books server doesn't serve).
//
// This mirrors the same env read in @shared/features/auth/useAuth (the auth
// short-circuit) — kept as one named export so the shell gates share a single
// source of truth.
export const IS_BOOKS_STANDALONE =
  import.meta.env.VITE_BOOKS_STANDALONE === "true";

// Product name shown in the standalone shell chrome (sidebar logo title +
// header page-title fallback). The host keeps "Boomtime".
export const STANDALONE_APP_NAME = "CatalystBooks";
