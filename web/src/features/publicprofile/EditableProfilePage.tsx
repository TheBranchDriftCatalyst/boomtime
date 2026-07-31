// EditableProfilePage — top-level wrapper for /p/:slug (gaka-ie3).
//
// Decides between three render modes based on the caller's identity vs the
// URL param:
//
//   - Anonymous visitor OR logged-in-but-not-owner  ->  <PublicDashboard/>
//     (byte-identical to the pre-change public view; a foreign viewer never
//      sees edit chrome).
//   - Owner in preview mode                        ->  <PublicDashboard/>
//     wrapped in a floating mode switch (Edit / Preview toggle).
//   - Owner in edit mode                           ->  <ProfileEditor/>
//     wrapped in the same mode switch.
//
// Ownership: compare the caller's public profile slug against the URL param.
// Public visitors hit `/p/:slug` without an auth cookie; the query is gated
// on `isLoggedIn` so anonymous visitors never issue the /users/current/
// profile fetch (which would 401 in the console).
//
// The URL never changes when switching modes — the toggle lives in
// component state.
import { lazy, Suspense, useState } from "react";
import { useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Spinner } from "@/components/Spinner";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { useAuth } from "@/features/auth/useAuth";
import { PublicDashboard } from "./PublicDashboard";
import { ProfileModeToggle, type ProfileMode } from "./ProfileModeToggle";

// Editor is lazy — the edit chrome + palette weighs more than a public view
// and only owners ever load it. Anonymous visitors + non-owners never touch
// this chunk.
const ProfileEditor = lazy(() =>
  import("./ProfileEditor").then((m) => ({ default: m.ProfileEditor })),
);

export function EditableProfilePage() {
  const { slug = "" } = useParams<{ slug: string }>();
  const { isLoggedIn, username } = useAuth();

  // Fetch the caller's own profile row so we can compare their slug to
  // :slug. Gated on isLoggedIn so anonymous visits stay clean (no 401 in
  // the network tab). staleTime is generous — the caller's own slug is
  // rarely changed and re-mounting the profile page shouldn't trigger a
  // network fetch.
  const { data: mine, isLoading: mineLoading } = useQuery({
    queryKey: qk.publicProfile(),
    queryFn: () => api.getPublicProfile(),
    enabled: isLoggedIn,
    staleTime: 30_000,
    retry: false,
  });

  // Ownership rule — the URL slug matches either:
  //   (a) the caller's configured public slug (primary signal), OR
  //   (b) the caller's username (fallback for users who own a profile row
  //       but haven't set a distinct slug — the server treats an empty
  //       slug column as "use username" behavior on the /p route).
  // Both must be lower-cased for case-insensitive matching because the
  // slug regex enforces lowercase but usernames don't.
  const isOwner =
    isLoggedIn &&
    slug !== "" &&
    ((mine?.slug ?? "").toLowerCase() === slug.toLowerCase() ||
      (mine?.slug == null && username.toLowerCase() === slug.toLowerCase()));

  // Mode toggle — Preview vs Edit. Owner only. Starts in Preview so an
  // owner landing on their own profile URL sees the same layout a visitor
  // would (no accidental edits from a nav-back). Explicit click to enter
  // edit mode.
  const [mode, setMode] = useState<ProfileMode>("preview");

  // Anonymous visitor OR still-resolving-auth OR non-owner: render the
  // read-only public view verbatim, without any owner chrome. This is the
  // non-negotiable path — public visitors must be byte-identical to the
  // pre-change behavior.
  if (!isLoggedIn || mineLoading || !isOwner) {
    return <PublicDashboard />;
  }

  // Owner path. Both edit and preview render inside the same shell so the
  // Edit/Preview switch can float above without depending on the child's
  // layout.
  return (
    <div className="relative">
      <ProfileModeToggle mode={mode} onChange={setMode} />
      {mode === "preview" ? (
        <PublicDashboard />
      ) : (
        <Suspense
          fallback={
            <div className="flex h-[60vh] items-center justify-center">
              <Spinner />
            </div>
          }
        >
          <ProfileEditor slug={slug} />
        </Suspense>
      )}
    </div>
  );
}
