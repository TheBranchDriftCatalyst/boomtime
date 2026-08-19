// gaka-ebq: shared hook for BOOM_ADMIN_USERS membership. Extracted so the
// Settings + Sidebar + AdminRoute all agree on ONE query key + selector for
// the current-user is_admin bit. Keeps the sidebar from flashing an Admin
// link on the first paint of a fresh page load: the hook returns
// `{ isAdmin, isLoading }` and callers can render `null` while loading to
// avoid a wrong-then-right transition.
//
// The underlying endpoint (`/auth/users/current`) is intentionally re-used
// with the SAME query key that Settings/Avatar already use, so react-query
// dedupes across mounts — one HTTP call feeds every consumer.
import { useQuery } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { useAuth } from "@shared/features/auth/useAuth";

export function useIsAdmin(): { isAdmin: boolean; isLoading: boolean } {
  const { isLoggedIn } = useAuth();
  const { data, isLoading } = useQuery({
    queryKey: ["auth", "current-user"],
    queryFn: () => api.currentUser(),
    // 60s matches the other call sites (Settings.tsx, AvatarTab.tsx). Long
    // enough that a tab switch doesn't refetch, short enough that a fresh
    // BOOM_ADMIN_USERS env flip on the server catches up within a minute.
    staleTime: 60_000,
    enabled: isLoggedIn,
    retry: false,
  });
  // is_admin is optional on the response (older servers don't emit it) —
  // treat missing as false so a stale binary can't accidentally grant admin.
  return { isAdmin: Boolean(data?.data?.is_admin), isLoading };
}
