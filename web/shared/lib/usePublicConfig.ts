import { useQuery } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { PublicConfig } from "@shared/types/api";

// Boot-time client config (gaka-93f.1.1). GET /api/v1/config/public tells the
// FE which auth provider is active, whether registration/billing are on, and
// which beta previews the server allows. It only changes on a server restart,
// so the query is cached hard (staleTime Infinity) and never refetched on
// focus — one fetch per app load.
//
// Consumers get a safe default while the (fast, unauthenticated) request is in
// flight so first paint never blocks on it.
const FALLBACK: PublicConfig = {
  registration_enabled: true,
  auth_provider: "local",
  oidc_enabled: false,
  billing_enabled: false,
  beta_flags: {},
  github_connect_enabled: false,
  books_enabled: false,
};

export function usePublicConfig(): { config: PublicConfig; isLoading: boolean } {
  const { data, isLoading } = useQuery({
    queryKey: qk.publicConfig(),
    queryFn: () => api.publicConfig(),
    staleTime: Infinity,
    gcTime: Infinity,
    refetchOnWindowFocus: false,
    retry: 1,
  });
  return { config: data ?? FALLBACK, isLoading };
}
