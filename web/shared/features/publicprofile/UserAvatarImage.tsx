// UserAvatarImage (boom-9v4) — a tiny <img> keyed to a username that
// gracefully falls back to an amber-bordered initials square when the
// server hasn't generated a chibi for that user yet.
//
// Twin of LabelImage.tsx (the shared per-archetype image component)
// but keyed on username instead of label id. The endpoint at
//   GET /api/v1/users/:username/avatar
// 404s when the row is missing OR status != 'ready' so the fallback path
// fires cleanly in the "user has never rendered" AND "render currently
// in flight" cases — the profile hero never has to know which is which.
//
// Cache-bust semantics: unlike LabelImage's immutable cache, the user
// avatar endpoint uses a modest 30s max-age server-side. That's usually
// enough for an iterative RENDER + reload flow — the caller can force
// an instant refresh by threading `bustHint` (typically the epoch of
// the last render completion).
import { useState } from "react";

export interface UserAvatarImageProps {
  /** Username — used to build the /api/v1/users/{username}/avatar URL. */
  username: string;
  /** Size in px (renders as a square). Default 240 (public profile hero). */
  size?: number;
  /** Optional cache-bust value (e.g. epoch of latest render). */
  bustHint?: string | number;
  /** Optional class name for the <img>. */
  className?: string;
  /** Alt text; defaults to `${username}'s chibi avatar`. */
  alt?: string;
}

// initials returns the 1-2 char glyph shown in the fallback square. Uses
// the first two ASCII letters of the username so "the.branchdrift" maps
// to "TB" and a single-letter username stays a single character.
function initials(username: string): string {
  const cleaned = username.replace(/[^a-zA-Z]/g, "").toUpperCase();
  if (cleaned.length === 0) return "?";
  if (cleaned.length === 1) return cleaned;
  return cleaned.slice(0, 2);
}

export function UserAvatarImage({
  username,
  size = 240,
  bustHint,
  className,
  alt,
}: UserAvatarImageProps) {
  const [errored, setErrored] = useState(false);

  // Fallback: initials in an amber-bordered square that visually matches
  // the arasaka dossier aesthetic (corner brackets, tight kerning). Used
  // when the endpoint 404s (never rendered / still running / error).
  if (errored) {
    return (
      <div
        data-testid="user-avatar-fallback"
        className={className}
        style={{
          width: size,
          height: size,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background: "color-mix(in oklab, var(--primary) 6%, transparent)",
          border: "1px solid color-mix(in oklab, var(--primary) 45%, transparent)",
          borderRadius: 2,
          fontFamily: '"Chakra Petch", "JetBrains Mono", ui-monospace, monospace',
          fontWeight: 700,
          letterSpacing: "0.14em",
          fontSize: Math.round(size * 0.4),
          color: "color-mix(in oklab, var(--primary) 85%, transparent)",
          textShadow: "0 0 12px color-mix(in oklab, var(--primary) 60%, transparent)",
        }}
        aria-label={alt ?? `${username} avatar placeholder`}
      >
        {initials(username)}
      </div>
    );
  }

  const bust =
    bustHint !== undefined
      ? `?v=${encodeURIComponent(String(bustHint))}`
      : "";
  const src = `/api/v1/users/${encodeURIComponent(username)}/avatar${bust}`;

  return (
    <img
      src={src}
      alt={alt ?? `${username}'s chibi avatar`}
      width={size}
      height={size}
      loading="lazy"
      decoding="async"
      className={className}
      onError={() => setErrored(true)}
      data-testid="user-avatar-image"
      data-username={username}
    />
  );
}
