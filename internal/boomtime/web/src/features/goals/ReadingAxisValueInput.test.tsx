// ReadingAxisValueInput tests (boom-wpb) — the reading-path mirror of the
// coding-path AxisValueInput autocomplete. Two load-bearing invariants:
//
//   - The datalist is populated from useReadingAxisValues — the user's DISTINCT
//     genre/series/status values render as <option>s under the value input.
//     A regression that stopped wiring the hook's options into the datalist
//     (e.g. reverting to a plain Input) drops every <option> and fails here.
//
//   - The datalist is SUGGEST-ONLY: a value NOT in the suggestion set still
//     commits via onChange (aspirational reading goals). This is the whole
//     point of a datalist over a Select — a regression to a restrict-to-list
//     control would swallow the typed value and fail.
//
// The hook is mocked so the assertion is about the WIRING (options → datalist,
// typed value → onChange), not about the query layer (covered separately by the
// hook's own contract). ReadingAxisValueInput is internal to PredicateBuilder,
// so we drive it through the public PredicateBuilder with a genre reading leaf.
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { PredicateBuilder } from "@boomtime/features/goals/PredicateBuilder";
import type { Predicate } from "@shared/types/api";

// Mock the reading-axis-values hook: deterministic distinct-value set, no
// network. Both tests read from this same list.
const MOCK_GENRES = ["Fiction", "Fantasy", "Science Fiction"];
vi.mock("@boomtime/features/goals/useReadingAxisValues", () => ({
  useReadingAxisValues: () => ({ options: MOCK_GENRES, isLoading: false }),
}));

// A genre-dimension reading leaf: renders the ReadingAxisValueInput value field.
function genreLeaf(value = ""): Predicate {
  return {
    kind: "time",
    source: "reading",
    axis: "genre",
    value,
    op: ">=",
    target_seconds: 5 * 3600,
    window: "week",
  };
}

function Harness({ initial }: { initial: Predicate }) {
  const [spec, setSpec] = useState<Predicate>(initial);
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  return (
    <QueryClientProvider client={qc}>
      <div>
        <PredicateBuilder node={spec} onChange={setSpec} />
        <pre data-testid="spec-json">{JSON.stringify(spec)}</pre>
      </div>
    </QueryClientProvider>
  );
}

function readSpec(): Predicate {
  return JSON.parse(screen.getByTestId("spec-json").textContent!) as Predicate;
}

describe("ReadingAxisValueInput", () => {
  it("renders a <datalist> option for each distinct value from the hook", () => {
    const { container } = render(<Harness initial={genreLeaf()} />);

    const input = screen.getByTestId("reading-dimension-value");
    const listId = input.getAttribute("list");
    // The value input must be wired to a datalist when suggestions exist.
    expect(listId).toBeTruthy();

    const options = Array.from(
      container.querySelectorAll<HTMLOptionElement>(`#${listId} option`),
    ).map((o) => o.value);
    expect(options).toEqual(MOCK_GENRES);
  });

  it("commits a typed value that is NOT in the suggestion list (suggest-only)", async () => {
    const user = userEvent.setup();
    render(<Harness initial={genreLeaf()} />);

    const input = screen.getByTestId("reading-dimension-value");
    // "Cyberpunk" is absent from MOCK_GENRES — a restrict-to-list control would
    // reject it; a datalist accepts it.
    await user.type(input, "Cyberpunk");

    const spec = readSpec() as Extract<Predicate, { kind: "time" }>;
    expect(spec.source).toBe("reading");
    expect(spec.axis).toBe("genre");
    expect(spec.value).toBe("Cyberpunk");
  });
});
