// SocialCardSection — a small "Social Card" panel in the profile editor
// (gaka social-card). Lets the owner preview and lightly customize the
// OpenGraph card that unfurls when they share their /p/:slug link.
//
// The card itself is the `social-card` widget rendered server-side to a
// 1200×630 PNG (GET /api/public/profile/:slug/og.png). This section shows
// that real image as the preview (so it matches exactly what Discord/Slack/
// Twitter render), plus the shareable link with a copy button and two light
// knobs — a theme picker (synthwave default) and an optional tagline that
// feeds og:description. Both persist through the existing profile-settings
// endpoint (PUT /api/v1/users/current/profile).
import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Check, Copy, RefreshCw, Share2 } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { api, ApiError } from "@/lib/api";
import { qk } from "@/lib/queryKeys";

const TAGLINE_MAX = 120;

export interface SocialCardSectionProps {
  slug: string;
}

export function SocialCardSection({ slug }: SocialCardSectionProps) {
  const qc = useQueryClient();

  const { data: profile } = useQuery({
    queryKey: qk.publicProfile(),
    queryFn: () => api.getPublicProfile(),
    staleTime: 30_000,
    retry: false,
  });

  // Draft knobs, seeded from the server value once it arrives.
  const [theme, setTheme] = useState<string>("");
  const [tagline, setTagline] = useState<string>("");
  useEffect(() => {
    if (profile) {
      setTheme(profile.cardTheme ?? "");
      setTagline(profile.cardTagline ?? "");
    }
  }, [profile]);

  const [saving, setSaving] = useState(false);
  const [copied, setCopied] = useState(false);
  // Bumped after a save so the <img> re-fetches the freshly-rendered card
  // (the endpoint caches for 10m, so a plain reload would show the old one).
  const [previewVersion, setPreviewVersion] = useState(0);

  const dirty =
    !!profile &&
    (theme !== (profile.cardTheme ?? "") ||
      tagline !== (profile.cardTagline ?? ""));

  const shareUrl = useMemo(() => {
    if (!slug) return "";
    const origin =
      typeof window !== "undefined" ? window.location.origin : "";
    return `${origin}/p/${slug}`;
  }, [slug]);

  const ogUrl = slug
    ? `/api/public/profile/${encodeURIComponent(slug)}/og.png?v=${previewVersion}`
    : "";

  const copy = async () => {
    if (!shareUrl) return;
    try {
      await navigator.clipboard.writeText(shareUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error("Copy failed — select and copy the link manually");
    }
  };

  const save = async () => {
    if (!profile || !dirty) return;
    setSaving(true);
    try {
      // Carry the current enable/slug so the toggle-only fields aren't reset;
      // the server leaves slug alone when enabled=false + slug="".
      await api.savePublicProfile({
        enabled: profile.enabled,
        slug: profile.slug ?? "",
        cardTheme: theme,
        cardTagline: tagline,
      });
      toast.success("Social card updated");
      qc.invalidateQueries({ queryKey: qk.publicProfile() });
      if (slug) qc.invalidateQueries({ queryKey: qk.publicDashboard(slug) });
      setPreviewVersion((v) => v + 1);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <section
      className="mb-4 rounded border border-border p-4"
      data-testid="social-card-section"
    >
      <div className="mb-3 flex items-center gap-2">
        <Share2 size={16} className="text-primary" />
        <h2 className="font-mono text-sm font-semibold uppercase tracking-[0.15em] text-primary">
          Social Card
        </h2>
        <span className="text-xs text-muted-foreground">
          how your link unfurls in Discord / Slack / Twitter
        </span>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1.4fr_1fr]">
        {/* Live preview = the actual rendered OG image */}
        <div className="overflow-hidden rounded border border-border/60 bg-background">
          {ogUrl ? (
            <img
              key={previewVersion}
              src={ogUrl}
              alt="Your social card preview"
              className="block aspect-[1200/630] w-full object-contain"
              data-testid="social-card-preview"
            />
          ) : (
            <div className="flex aspect-[1200/630] items-center justify-center text-xs text-muted-foreground">
              Set a public slug to generate your card.
            </div>
          )}
        </div>

        {/* Controls */}
        <div className="flex flex-col gap-3">
          {/* Shareable link + copy */}
          <div>
            <label className="mb-1 block font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground">
              Shareable link
            </label>
            <div className="flex items-center gap-2">
              <input
                readOnly
                value={shareUrl}
                onFocus={(e) => e.currentTarget.select()}
                className="min-w-0 flex-1 truncate rounded border border-border bg-background px-2 py-1 font-mono text-xs"
                data-testid="social-card-link"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={copy}
                disabled={!shareUrl}
                data-testid="social-card-copy"
                title="Copy link"
              >
                {copied ? <Check size={13} /> : <Copy size={13} />}
              </Button>
            </div>
          </div>

          {/* Theme picker */}
          <div>
            <label
              htmlFor="social-card-theme"
              className="mb-1 block font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground"
            >
              Theme
            </label>
            <select
              id="social-card-theme"
              value={theme}
              onChange={(e) => setTheme(e.target.value)}
              className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm"
              data-testid="social-card-theme"
            >
              <option value="">Synthwave (default)</option>
              <option value="dark">Synthwave dark</option>
              <option value="light">Light</option>
            </select>
          </div>

          {/* Tagline */}
          <div>
            <label
              htmlFor="social-card-tagline"
              className="mb-1 flex items-center justify-between font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground"
            >
              <span>Tagline</span>
              <span className="tabular-nums">
                {tagline.length}/{TAGLINE_MAX}
              </span>
            </label>
            <input
              id="social-card-tagline"
              value={tagline}
              maxLength={TAGLINE_MAX}
              onChange={(e) => setTagline(e.target.value)}
              placeholder="shipping boomtime one heartbeat at a time"
              className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm"
              data-testid="social-card-tagline"
            />
            <p className="mt-1 text-[10px] text-muted-foreground">
              Shown on the card and used as the share description. Leave blank to
              auto-build one from your stats.
            </p>
          </div>

          <div className="flex items-center gap-2">
            <Button
              type="button"
              size="sm"
              onClick={save}
              disabled={saving || !dirty}
              data-testid="social-card-save"
            >
              {saving ? "Saving…" : "Save card"}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setPreviewVersion((v) => v + 1)}
              title="Reload preview"
              data-testid="social-card-refresh"
            >
              <RefreshCw size={13} />
            </Button>
          </div>
        </div>
      </div>
    </section>
  );
}
