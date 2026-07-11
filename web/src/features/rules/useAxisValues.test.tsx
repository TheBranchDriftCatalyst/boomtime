import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAxisValues } from "@/hooks/useAxisValues";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import { groupPayload } from "@/test/factories";

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe("useAxisValues (P1)", () => {
  it("maps groups -> options, drops null/'' buckets, sorts by count desc", async () => {
    server.use(
      http.get("/api/v1/users/current/heartbeats/group", () =>
        HttpResponse.json(
          groupPayload({
            groups: [
              { value: "low", count: 2, seconds: 0, firstSeen: "", lastSeen: "" },
              { value: "high", count: 50, seconds: 0, firstSeen: "", lastSeen: "" },
              { value: null, count: 999, seconds: 0, firstSeen: "", lastSeen: "" },
              { value: "", count: 7, seconds: 0, firstSeen: "", lastSeen: "" },
            ],
          }),
        ),
      ),
    );

    const { result } = renderHook(() => useAxisValues("project"), {
      wrapper: wrapper(),
    });

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.options).toEqual([
      { value: "high", count: 50 },
      { value: "low", count: 2 },
    ]);
  });

  it("is disabled (no fetch) when axis is null", async () => {
    const { result } = renderHook(() => useAxisValues(null), {
      wrapper: wrapper(),
    });
    // Disabled query never loads; options stay empty.
    expect(result.current.options).toEqual([]);
  });
});
