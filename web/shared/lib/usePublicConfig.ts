import { useQuery } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { PublicConfig } from "@shared/types/api";
import { IS_BOOKS_STANDALONE } from "@shared/lib/standalone";

// Boot-time client config (boom-93f.1.1). GET /api/v1/config/public tells the
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

// Standalone books app: there is NO /api/v1/config/public endpoint (the whole app
// IS books), so the fetch would 404 and fall back to books_enabled:false — hiding
// the Books nav + showing "Books isn't enabled". Report a fixed standalone config
// (books on, no registration/oidc/github) and skip the doomed request.
const STANDALONE_CONFIG: PublicConfig = {
  registration_enabled: false,
  auth_provider: "local",
  oidc_enabled: false,
  billing_enabled: false,
  beta_flags: {},
  github_connect_enabled: false,
  books_enabled: true,
};

export function usePublicConfig(): { config: PublicConfig; isLoading: boolean } {
  const { data, isLoading } = useQuery({
    queryKey: qk.publicConfig(),
    queryFn: () => api.publicConfig(),
    staleTime: Infinity,
    gcTime: Infinity,
    refetchOnWindowFocus: false,
    retry: 1,
    enabled: !IS_BOOKS_STANDALONE,
  });
  if (IS_BOOKS_STANDALONE) return { config: STANDALONE_CONFIG, isLoading: false };
  return { config: data ?? FALLBACK, isLoading };
}
