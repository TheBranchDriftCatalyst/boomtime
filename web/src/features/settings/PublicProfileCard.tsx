// PublicProfileCard — Settings tab card that owns the public-profile
// enable-toggle + slug for the caller (gaka-6jm.1).
//
// Contract:
//   - Reads via GET /api/v1/users/current/profile on mount.
//   - Saves via PUT /api/v1/users/current/profile. Server enforces the
//     slug regex, blocks reserved names, and returns 409 on slug conflict.
//   - On success invalidates qk.publicProfile() so the Sidebar's copy of
//     the toggle refreshes atomically and the "Public profile" nav link
//     appears/disappears.
//
// The client-side Zod slug regex MUST stay in sync with the server one in
// internal/handler/profile.go — a mismatch would surface as a 400 with a
// generic message and confuse users.
import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Check, Copy, ExternalLink } from "lucide-react";
import { z } from "zod";
import { api, ApiError } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { Switch } from "@thebranchdriftcatalyst/catalyst-ui/ui/switch";

// MUST mirror internal/handler/profile.go: publicProfileSlugRe. Kept
// verbatim (regex + message) so client-side validation matches server
// behavior 1:1 — otherwise a valid slug could get rejected only after a
// round-trip, or vice versa.
const SLUG_RE = /^[a-z0-9]([a-z0-9-]{1,28}[a-z0-9])?$/;

const schema = z.object({
  slug: z
    .string()
    .regex(
      SLUG_RE,
      "Slug must be 3-30 characters, lowercase letters/digits/hyphens (no leading/trailing hyphen)",
    ),
});
type FormValues = z.infer<typeof schema>;

export function PublicProfileCard() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: qk.publicProfile(),
    queryFn: () => api.getPublicProfile(),
    staleTime: 30_000,
  });

  // The enabled toggle is local state seeded from the server value. Kept
  // separate from the form so users can toggle without triggering a form
  // reset — the Save button submits both together.
  const [enabled, setEnabled] = useState(false);
  const [copied, setCopied] = useState(false);
  const [serverError, setServerError] = useState<string>("");

  const {
    register,
    handleSubmit,
    reset,
    watch,
    formState: { errors },
  } = useForm<FormValues>({ defaultValues: { slug: "" } });

  // Seed local state from the server payload on load / refetch. The reset
  // uses the server slug so the form's dirty-tracking is honest about
  // whether the user has actually edited the field.
  useEffect(() => {
    if (data) {
      setEnabled(data.enabled);
      reset({ slug: data.slug ?? "" });
    }
  }, [data, reset]);

  const currentSlug = watch("slug");
  const publicUrl =
    typeof window !== "undefined" && currentSlug
      ? `${window.location.origin}/p/${currentSlug}`
      : "";

  const mutate = useMutation({
    mutationFn: (body: { enabled: boolean; slug: string }) =>
      api.savePublicProfile(body),
    onSuccess: (resp) => {
      toast.success("Public profile updated");
      setServerError("");
      // Invalidate so the Sidebar (also watching qk.publicProfile()) picks
      // up the new enabled state and the nav link appears/disappears.
      qc.invalidateQueries({ queryKey: qk.publicProfile() });
      reset({ slug: resp.slug ?? "" });
      setEnabled(resp.enabled);
    },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        setServerError("That slug is already taken — try another.");
        return;
      }
      setServerError(e instanceof ApiError ? e.message : "Unknown error");
    },
  });

  async function onSubmit(values: FormValues) {
    // When disabling with no slug set, send empty string (server treats
    // that as "leave slug alone"). Otherwise require a valid slug.
    if (enabled) {
      const parsed = schema.safeParse(values);
      if (!parsed.success) {
        setServerError(parsed.error.issues[0]?.message ?? "Invalid slug");
        return;
      }
    }
    setServerError("");
    mutate.mutate({ enabled, slug: values.slug ?? "" });
  }

  async function onCopy() {
    try {
      await navigator.clipboard.writeText(publicUrl);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error("Could not copy — copy manually.");
    }
  }

  return (
    <Card data-testid="public-profile-card">
      <CardHeader className="p-4 pb-0">
        <h2 className="text-lg font-semibold">Public profile</h2>
        <p className="text-sm text-muted-foreground">
          Enable a read-only version of your dashboard at a shareable URL. The
          public page respects your Hidden Data rules — hidden projects,
          languages, and machines never appear.
        </p>
      </CardHeader>
      <CardContent className="p-4">
        <form
          onSubmit={handleSubmit(onSubmit)}
          className="max-w-lg space-y-4"
          aria-label="Public profile form"
        >
          {/* Enable / disable toggle */}
          <div className="flex items-center justify-between rounded-lg border border-border p-3">
            <div className="space-y-0.5">
              <Label htmlFor="public-enabled" className="text-sm font-medium">
                Enable public profile
              </Label>
              <p className="text-xs text-muted-foreground">
                When on, your dashboard is visible at{" "}
                <code className="rounded bg-muted px-1 py-0.5 text-[11px]">
                  /p/&lt;slug&gt;
                </code>{" "}
                to anyone with the link.
              </p>
            </div>
            <Switch
              id="public-enabled"
              data-testid="public-profile-switch"
              checked={enabled}
              onCheckedChange={setEnabled}
              disabled={isLoading}
            />
          </div>

          {/* Slug field. Rendered even when disabled so users can pre-set the
              slug before flipping the switch on. */}
          <div className="space-y-1.5">
            <Label htmlFor="public-slug">Slug</Label>
            <Input
              id="public-slug"
              data-testid="public-profile-slug"
              placeholder="pandax"
              autoComplete="off"
              spellCheck={false}
              {...register("slug")}
            />
            {errors.slug && (
              <p className="text-xs text-destructive">{errors.slug.message}</p>
            )}
            <p className="text-xs text-muted-foreground">
              3-30 lowercase letters, digits, hyphens. No leading/trailing hyphen.
            </p>
          </div>

          {/* Public URL preview + copy + open-preview link, shown when we have
              a valid-looking slug and the server has confirmed the user is
              currently enabled (avoids advertising a URL that returns 404). */}
          {currentSlug && enabled && data?.enabled && data?.slug === currentSlug && (
            <div className="space-y-2 rounded-lg border border-border bg-muted/40 p-3">
              <Label className="text-xs uppercase tracking-wide text-muted-foreground">
                Public URL
              </Label>
              <div className="flex items-center gap-2">
                <code
                  className="flex-1 truncate rounded bg-background px-2 py-1 text-sm"
                  title={publicUrl}
                >
                  {publicUrl}
                </code>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={onCopy}
                  aria-label="Copy public URL"
                >
                  {copied ? (
                    <Check className="h-3.5 w-3.5" />
                  ) : (
                    <Copy className="h-3.5 w-3.5" />
                  )}
                </Button>
                <a
                  href={`/p/${currentSlug}`}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 text-sm text-primary hover:underline"
                >
                  Preview <ExternalLink className="h-3.5 w-3.5" />
                </a>
              </div>
            </div>
          )}

          <div className="flex items-center gap-3">
            <Button
              type="submit"
              disabled={mutate.isPending || isLoading}
              data-testid="public-profile-submit"
            >
              {mutate.isPending ? "Saving..." : "Save"}
            </Button>
            {serverError && (
              <p className="text-sm text-destructive" role="alert">
                {serverError}
              </p>
            )}
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
