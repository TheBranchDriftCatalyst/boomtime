import type { ReactElement, ReactNode } from "react";
import { render, type RenderOptions } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { ThemeProvider } from "@/theme/ThemeProvider";
import { RendererProvider } from "@/viz/RendererProvider";
import { AuthProvider } from "@/hooks/useAuth";

/** A QueryClient with retries + refetch off, so tests are deterministic. */
export function makeTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
}

interface ProvidersProps {
  children: ReactNode;
  queryClient?: QueryClient;
  /** Include the AuthProvider (which bootstraps via /auth/refresh_token). */
  withAuth?: boolean;
  /** Wrap in a MemoryRouter (needed for router-dependent components). */
  withRouter?: boolean;
  initialEntries?: string[];
}

export function Providers({
  children,
  queryClient,
  withAuth = false,
  withRouter = false,
  initialEntries = ["/"],
}: ProvidersProps) {
  const qc = queryClient ?? makeTestQueryClient();

  let tree: ReactNode = (
    <ThemeProvider>
      <RendererProvider>
        <QueryClientProvider client={qc}>{children}</QueryClientProvider>
      </RendererProvider>
    </ThemeProvider>
  );

  if (withAuth) {
    tree = (
      <ThemeProvider>
        <RendererProvider>
          <QueryClientProvider client={qc}>
            <AuthProvider>{children}</AuthProvider>
          </QueryClientProvider>
        </RendererProvider>
      </ThemeProvider>
    );
  }

  if (withRouter) {
    return <MemoryRouter initialEntries={initialEntries}>{tree}</MemoryRouter>;
  }
  return <>{tree}</>;
}

interface RenderWithProvidersOptions extends Omit<RenderOptions, "wrapper"> {
  queryClient?: QueryClient;
  withAuth?: boolean;
  withRouter?: boolean;
  initialEntries?: string[];
}

/**
 * Shared render helper — every component test uses this so there's zero
 * per-test provider boilerplate. Returns the RTL result plus the QueryClient
 * (for asserting cache invalidation, etc.).
 */
export function renderWithProviders(
  ui: ReactElement,
  {
    queryClient,
    withAuth,
    withRouter,
    initialEntries,
    ...options
  }: RenderWithProvidersOptions = {},
) {
  const qc = queryClient ?? makeTestQueryClient();
  const result = render(ui, {
    wrapper: ({ children }) => (
      <Providers
        queryClient={qc}
        withAuth={withAuth}
        withRouter={withRouter}
        initialEntries={initialEntries}
      >
        {children}
      </Providers>
    ),
    ...options,
  });
  return { ...result, queryClient: qc };
}
