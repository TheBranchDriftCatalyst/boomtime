// LabelImage — a tiny <img> that gracefully falls back to a glyph span
// (or the provided fallback node) when the backend hasn't generated an
// image for this label (gaka-myv).
//
// The image URL is /api/v1/labels/{id}/image; the server responds 404
// when no row is present, which triggers onError -> fallback state.
// Reads DO NOT hit the auth path — the endpoint is intentionally public.
//
// Cache-bust semantics: the public endpoint is `immutable` for 1 year, so
// after an admin regenerates a label the browser would happily keep
// serving the stale bytes. The FE therefore appends ?v=<hash-of-id>
// (stable, non-timestamp-based) — the URL naturally changes on every
// admin regen because... actually no, `id` doesn't change. So the caller
// can pass an optional `bustHint` prop (typically the epoch of the last
// regenerate). Absent hint → cache is stable-and-immutable, which is the
// steady-state we want; opt into busting by threading through the hint.
import { useState } from "react";

export interface LabelImageProps {
  /** Label id — used to build the /api/v1/labels/{id}/image URL. */
  id: string;
  /** Rendered when the image 404s or errors. Typically the glyph span. */
  fallback: React.ReactNode;
  /** Optional cache-bust query value (e.g. `?v=<epoch>`). */
  bustHint?: string | number;
  /** Size in px. Default 20 (matches the chip glyph size). */
  size?: number;
  /** Optional class name for the <img>. */
  className?: string;
  /** Alt text (screen readers). Defaults to empty (decorative). */
  alt?: string;
}

export function LabelImage({
  id,
  fallback,
  bustHint,
  size = 20,
  className,
  alt = "",
}: LabelImageProps) {
  const [errored, setErrored] = useState(false);
  if (errored) return <>{fallback}</>;

  const bust = bustHint !== undefined ? `?v=${encodeURIComponent(String(bustHint))}` : "";
  const src = `/api/v1/labels/${encodeURIComponent(id)}/image${bust}`;

  return (
    <img
      src={src}
      alt={alt}
      width={size}
      height={size}
      loading="lazy"
      decoding="async"
      className={className}
      onError={() => setErrored(true)}
      data-testid="label-image"
      data-label-id={id}
    />
  );
}
