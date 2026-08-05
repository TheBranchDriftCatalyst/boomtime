// InAppProfilePage — the owner's profile dashboard mounted INSIDE the app
// skeleton (gaka-4ng). Route: /app/profile (under AppShell → sidebar + header).
//
// This is the owner's default way to view + edit their profile, unified with
// the other in-app dashboards: the full dossier chrome (hero, classification
// strip, corner marks, ReclassifyOverlay) renders inside <Page.Content>, with
// the same Edit/Preview toggle as /p/:slug. Unlike EditableProfilePage there's
// no :slug param and no ownership branch — the /app tree is auth-guarded, so
// the viewer is always the owner; we resolve THEIR own slug and render as owner.
//
// The standalone public /p/:slug route (EditableProfilePage) is unchanged — it
// stays the shareable page for logged-out/external visitors.
import { lazy, Suspense, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { useAuth } from "@/features/auth/useAuth";
import { Page } from "@/layout/Page";
import { PublicDashboard } from "./PublicDashboard";
import { ProfileModeToggle, type ProfileMode } from "./ProfileModeToggle";

const ProfileEditor = lazy(() =>
  import("./ProfileEditor").then((m) => ({ default: m.ProfileEditor })),
);

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-[60vh] items-center justify-center">{children}</div>
  );
}

export function InAppProfilePage() {
  const { username } = useAuth();

  // The owner's own profile row → their slug. Same query key/staleTime as
  // EditableProfilePage so the two share cache. A null slug column means the
  // server falls back to username on the /p route, so mirror that here.
  const { data: mine, isLoading } = useQuery({
    queryKey: qk.publicProfile(),
    queryFn: () => api.getPublicProfile(),
    staleTime: 30_000,
    retry: false,
  });

  const [mode, setMode] = useState<ProfileMode>("preview");

  const slug = (mine?.slug ?? username ?? "").trim();

  return (
    <Page>
      <Page.Content>
        {isLoading ? (
          <Centered>
            <Spinner />
          </Centered>
        ) : (
          // `transform` establishes a containing block so the dossier's
          // position:fixed grid/scanline overlays (arasaka/dossier css) and the
          // floating mode toggle stay INSIDE the app content area instead of
          // escaping over the sidebar/header. gaka-4ng — needs visual QA.
          <div className="relative" style={{ transform: "translateZ(0)" }}>
            <ProfileModeToggle mode={mode} onChange={setMode} />
            {mode === "preview" ? (
              <PublicDashboard slug={slug} />
            ) : (
              <Suspense fallback={<Centered><Spinner /></Centered>}>
                <ProfileEditor slug={slug} />
              </Suspense>
            )}
          </div>
        )}
      </Page.Content>
    </Page>
  );
}
