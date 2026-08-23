import type { ReactElement, ReactNode } from "react";
import { render, type RenderOptions } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { ThemeProvider } from "@thebranchdriftcatalyst/catalyst-ui/contexts/Theme";
import { AuthProvider } from "@shared/features/auth/useAuth";

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

  const inner: ReactNode = withAuth ? (
    <AuthProvider>{children}</AuthProvider>
  ) : (
    children
  );

  const wrapped: ReactNode = (
    <ThemeProvider>
      <QueryClientProvider client={qc}>{inner}</QueryClientProvider>
    </ThemeProvider>
  );

  if (withRouter) {
    // boom-ie3: build a data router via createMemoryRouter so tests can
    // exercise data-router-only APIs (unstable_usePrompt in ProfileEditor
    // + any future useBlocker use). A catchall route wraps `wrapped` so
    // useParams-consuming components can be tested by passing initial
    // entries like ["/p/panda"] — the calling test wraps its own
    // <Route path=":slug"> inside `children` when it cares.
    const router = createMemoryRouter([{ path: "*", element: wrapped }], {
      initialEntries,
    });
    return <RouterProvider router={router} />;
  }
  return <>{wrapped}</>;
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
