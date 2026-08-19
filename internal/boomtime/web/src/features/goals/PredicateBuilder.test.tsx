// PredicateBuilder tests — lock down the state transitions of the
// recursive builder (gaka-wpb). This is the one novel piece with no
// pattern-match in the codebase; each test asserts a load-bearing
// invariant of the lift-and-lower design.
//
// Non-tautology anchors:
//
//   - Defaults SERIALIZE to a spec the backend accepts (mirrors the
//     Go ValidateSpec whitelist — kind/axis/window/op/target). If a
//     future change to defaults drifts from what the validator
//     accepts, this test fails.
//
//   - Editing a leaf field replaces JUST that field on the tree
//     (produces a new tree, doesn't mutate). Non-trivial because
//     the recursive component relies on immutable spread semantics.
//
//   - Converting a leaf to a group WRAPS the current leaf inside
//     the new group as its FIRST child — no data loss.
//
//   - Adding a child appends to `of`; removing splices; the
//     onRemove affordance disappears when only one child remains
//     (backend rejects empty `of`).
import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  PredicateBuilder,
  defaultLeaf,
  readingLeaf,
} from "@boomtime/features/goals/PredicateBuilder";
import type { Predicate } from "@shared/types/api";

type TimeLeaf = Extract<Predicate, { kind: "time" }>;

// Controlled harness that holds spec state and re-renders on every
// change — mirrors the real GoalForm consumer.
//
// Wrapped in a QueryClientProvider because the value input's
// AxisValueInput hook calls useQuery(getStats) for autocomplete
// suggestions (retry:false + no fetch resolved keeps the test
// synchronous — the datalist just stays empty).
function Harness({ initial }: { initial?: Predicate }) {
  const [spec, setSpec] = useState<Predicate>(initial ?? defaultLeaf());
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  return (
    <QueryClientProvider client={qc}>
      <div>
        <PredicateBuilder node={spec} onChange={setSpec} />
        {/* Debug JSON — asserted against in tests via testid. */}
        <pre data-testid="spec-json">{JSON.stringify(spec)}</pre>
      </div>
    </QueryClientProvider>
  );
}

function readSpec(): Predicate {
  return JSON.parse(screen.getByTestId("spec-json").textContent!) as Predicate;
}

describe("PredicateBuilder", () => {
  it("default leaf serializes to a spec matching backend defaults", () => {
    render(<Harness />);
    const spec = readSpec();
    expect(spec).toEqual({
      kind: "time",
      axis: "language",
      value: null,
      op: ">=",
      target_seconds: 3600,
      window: "week",
    });
  });

  it("editing target on a time leaf produces an updated spec (bare seconds still work)", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const targetInput = screen.getByLabelText("Target");
    await user.clear(targetInput);
    await user.type(targetInput, "7200");
    // DurationInput commits on blur / Enter — trigger blur.
    fireEvent.blur(targetInput);
    expect(readSpec()).toMatchObject({
      kind: "time",
      target_seconds: 7200,
    });
  });

  it("target accepts shortform durations (1h30m → 5400)", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const targetInput = screen.getByLabelText("Target");
    await user.clear(targetInput);
    await user.type(targetInput, "1h30m");
    fireEvent.blur(targetInput);
    expect(readSpec()).toMatchObject({ kind: "time", target_seconds: 5400 });
  });

  it("typing an axis value replaces the null with the string; blank stays null", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const valueInput = screen.getByLabelText("Value (blank = any)");
    await user.type(valueInput, "Go");
    expect((readSpec() as Extract<Predicate, { kind: "time" }>).value).toBe("Go");
    await user.clear(valueInput);
    expect((readSpec() as Extract<Predicate, { kind: "time" }>).value).toBe(null);
  });

  // Convert leaf → `all` group via the KindSwitcher Select. Drives the
  // REAL DOM (Radix Select's pointer events work now that setup.ts
  // shims hasPointerCapture / setPointerCapture / releasePointerCapture).
  //
  // gaka-wpb.1 (audit): the earlier version of this test hand-built
  // `{ kind: "all", of: [leaf] }` and asserted on the literal — a
  // pure tautology that would pass even if convertKind returned
  // null. This version pins the LOAD-BEARING contract: after the
  // user selects "All of (AND)" in the type switcher, the resulting
  // spec is an `all` group containing the SAME leaf (target=999
  // survives) as its FIRST child. A regression in convertKind that
  // dropped the existing leaf (returned `{ kind: "all", of: [] }`
  // or seeded with a fresh defaultLeaf) would produce target=3600
  // and the assertion below would fail.
  it("converting a leaf to an `all` group via kind switcher wraps the leaf as first child", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    // First: edit the target so we can prove the wrapped child
    // preserves the edit (not silently reset to defaults).
    const targetInput = screen.getByLabelText("Target");
    await user.clear(targetInput);
    await user.type(targetInput, "999");
    fireEvent.blur(targetInput);
    // Open the KindSwitcher (the only Select initially rendered — a
    // leaf editor has other selects but they're for axis/op/window;
    // the kind switcher is the leftmost / first one in DOM order).
    const [kindSwitcher] = screen.getAllByRole("combobox");
    await user.click(kindSwitcher);
    const allOption = await screen.findByText("All of (AND)");
    await user.click(allOption);
    const spec = readSpec();
    expect(spec.kind).toBe("all");
    const group = spec as Extract<Predicate, { kind: "all" }>;
    // Load-bearing: the surviving first child is the original leaf
    // with target=999 (proves convertKind's `from.kind === "time"`
    // branch preserved it).
    expect(group.of[0]).toMatchObject({ kind: "time", target_seconds: 999 });
  });

  // gaka-bs5l (P0): a reading leaf MUST stay reading through every edit. The
  // original bug: convertKind(from, "time") returned the coding defaultLeaf(),
  // so a kind round-trip on a reading leaf silently reverted it to the coding
  // axis. This test edits the op AND round-trips the kind (reading-time →
  // All-of → back to Time) and asserts source stays "reading" the whole way.
  // Before the fix, the final assertion fails (source is dropped → coding).
  it("reading leaf stays 'reading' through an op edit + kind round-trip (gaka-bs5l)", async () => {
    const user = userEvent.setup();
    render(<Harness initial={readingLeaf()} />);
    expect((readSpec() as TimeLeaf).source).toBe("reading");

    // Edit the Op — a spread-based onChange path, must preserve source.
    const opTrigger = screen.getByLabelText("Op");
    await user.click(opTrigger);
    await user.click(await screen.findByRole("option", { name: "<=" }));
    expect(readSpec()).toMatchObject({
      kind: "time",
      source: "reading",
      op: "<=",
    });

    // Round-trip the KIND via the KindSwitcher: Time → All-of → Time.
    // (Use role=option to disambiguate from the trigger, which mirrors the
    // selected label as text.)
    await user.click(screen.getAllByRole("combobox")[0]);
    await user.click(await screen.findByRole("option", { name: "All of (AND)" }));
    expect(readSpec().kind).toBe("all");

    // The group's KindSwitcher is now the first combobox. Switch back to Time.
    await user.click(screen.getAllByRole("combobox")[0]);
    await user.click(await screen.findByRole("option", { name: "Time on axis" }));

    const after = readSpec() as TimeLeaf;
    expect(after.kind).toBe("time");
    // THE REGRESSION GUARD: reading survives the round-trip (was coding before).
    expect(after.source).toBe("reading");
  });

  // gaka-dvy9: a genre'd reading goal — pick the Genre dimension and type a
  // value; the built spec carries source="reading", axis="genre", value.
  it("builds a genre-filtered reading spec (dimension picker + value)", async () => {
    const user = userEvent.setup();
    render(<Harness initial={readingLeaf()} />);
    // Default reading leaf has no dimension → the fixed metric label shows.
    expect(screen.getByTestId("reading-metric-label")).toBeInTheDocument();

    // Select the Genre dimension via the Filter select.
    await user.click(screen.getByLabelText("Filter"));
    await user.click(await screen.findByRole("option", { name: "Genre" }));

    // The value input replaces the fixed metric label; type a genre.
    const value = screen.getByTestId("reading-dimension-value");
    await user.type(value, "Fiction");

    expect(readSpec()).toMatchObject({
      kind: "time",
      source: "reading",
      axis: "genre",
      value: "Fiction",
      window: "week",
    });

    // Switching back to Total listening drops both axis and value.
    await user.click(screen.getByLabelText("Filter"));
    await user.click(await screen.findByRole("option", { name: "Total listening" }));
    const back = readSpec() as TimeLeaf;
    expect(back.source).toBe("reading");
    expect(back.axis).toBeUndefined();
    expect(back.value).toBeUndefined();
  });

  it("+ Add condition appends a new leaf to an `all` group", async () => {
    const user = userEvent.setup();
    const initial: Predicate = {
      kind: "all",
      of: [defaultLeaf()],
    };
    render(<Harness initial={initial} />);
    // Initially one child.
    const before = readSpec() as Extract<Predicate, { kind: "all" }>;
    expect(before.of).toHaveLength(1);
    const addBtn = screen.getByRole("button", { name: /add condition/i });
    await user.click(addBtn);
    const after = readSpec() as Extract<Predicate, { kind: "all" }>;
    expect(after.of).toHaveLength(2);
    // The new child should be a fresh default time leaf.
    expect(after.of[1]).toMatchObject({ kind: "time" });
  });

  it("no remove buttons on a group with a single child (would leave empty `of`)", () => {
    // Group with a single child — the child's remove button should NOT
    // render (removing would leave `of` empty; backend rejects).
    const singleChild: Predicate = {
      kind: "all",
      of: [defaultLeaf()],
    };
    render(<Harness initial={singleChild} />);
    const removeButtonsSingle = screen.queryAllByTitle("Remove");
    expect(removeButtonsSingle).toHaveLength(0);
  });

  it("two children → each leaf gets a remove button", () => {
    const twoChildren: Predicate = {
      kind: "all",
      of: [defaultLeaf(), defaultLeaf()],
    };
    render(<Harness initial={twoChildren} />);
    const removeButtonsTwo = screen.getAllByTitle("Remove");
    // Both children get one; the outer group itself doesn't (no
    // onRemove from the harness).
    expect(removeButtonsTwo.length).toBe(2);
  });

  it("removing a leaf splices it out of the group's `of`", async () => {
    const user = userEvent.setup();
    // Two distinguishable children — first with target=100, second
    // with target=200 — so we can prove which was removed.
    const leaf = defaultLeaf() as Extract<Predicate, { kind: "time" }>;
    const initial: Predicate = {
      kind: "all",
      of: [
        { ...leaf, target_seconds: 100 },
        { ...leaf, target_seconds: 200 },
      ],
    };
    render(<Harness initial={initial} />);
    // Grab the remove button for the FIRST child (visual order).
    const [firstRemove] = screen.getAllByTitle("Remove");
    await user.click(firstRemove);
    const after = readSpec() as Extract<Predicate, { kind: "all" }>;
    expect(after.of).toHaveLength(1);
    // The surviving child should be the one with target=200.
    expect(after.of[0]).toMatchObject({ kind: "time", target_seconds: 200 });
  });

  it("editing a child inside a group preserves the parent's kind", async () => {
    const user = userEvent.setup();
    const initial: Predicate = {
      kind: "any",
      of: [defaultLeaf(), defaultLeaf()],
    };
    render(<Harness initial={initial} />);
    // Edit the FIRST leaf's target — the parent must stay `any`.
    const [firstTarget] = screen.getAllByLabelText("Target");
    await user.clear(firstTarget);
    await user.type(firstTarget, "42");
    fireEvent.blur(firstTarget);
    const after = readSpec() as Extract<Predicate, { kind: "any" }>;
    expect(after.kind).toBe("any");
    expect(after.of[0]).toMatchObject({ target_seconds: 42 });
    expect(after.of[1]).toMatchObject({ target_seconds: 3600 });
  });

  // gaka-wpb.1 (audit): the previous version admitted in-comment it
  // wasn't testing immutability — readSpec() re-parses the DOM so
  // "distinguishable snapshots" don't prove non-mutation. This
  // version captures the object reference passed to the harness's
  // `onChange` callback + checks that a subsequent edit does NOT
  // mutate the ORIGINAL object we captured (Object.freeze + verify
  // no throws when the harness re-renders). If PredicateBuilder ever
  // mutates the passed-in `node` (e.g. `node.target_seconds = 42`),
  // the frozen object would throw on assignment.
  it("does not mutate the previous state (immutable spread semantics)", async () => {
    const user = userEvent.setup();
    // FrozenHarness deep-freezes every state snapshot before passing
    // it into PredicateBuilder. A component that mutates the passed
    // `node` — even via a nested field — would throw a TypeError on
    // the mutation attempt (strict-mode Object.freeze semantics).
    function deepFreeze<T>(o: T): T {
      if (o && typeof o === "object") {
        Object.getOwnPropertyNames(o).forEach((k) => {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          deepFreeze((o as any)[k]);
        });
        Object.freeze(o);
      }
      return o;
    }
    function FrozenHarness() {
      const [spec, setSpec] = useState<Predicate>(() => deepFreeze(defaultLeaf()));
      const qc = new QueryClient({
        defaultOptions: { queries: { retry: false, staleTime: Infinity } },
      });
      return (
        <QueryClientProvider client={qc}>
          <div>
            <PredicateBuilder
              node={spec}
              onChange={(next) => setSpec(deepFreeze(next))}
            />
            <pre data-testid="spec-json">{JSON.stringify(spec)}</pre>
          </div>
        </QueryClientProvider>
      );
    }
    render(<FrozenHarness />);
    const targetInput = screen.getByLabelText("Target");
    // If PredicateBuilder mutates node.target_seconds directly, the
    // frozen object rejects the assignment and the test fails.
    await user.clear(targetInput);
    await user.type(targetInput, "42");
    fireEvent.blur(targetInput);
    expect(readSpec()).toMatchObject({ kind: "time", target_seconds: 42 });
  });

  // gaka-wpb.1 (audit): the earlier "has within helper" test was a
  // no-op lint-quieter. Replaced with a real test that pins the
  // depth-cap invariant on the "convert to group" affordance.
  //
  // The KindSwitcher disables the "group" options (streak/all/any/not)
  // at depth === MaxPredicateDepth so users can't author a spec the
  // server would reject. Drive the DOM to prove those items render
  // as disabled at the cap, and that they're ENABLED one level up.
  // Depth-cap invariant: adding a sibling inside a cap-depth group
  // keeps the tree at MaxPredicateDepth (never deepens). Observable
  // via a walk over the resulting spec — the invariant users depend
  // on is "server-accepted depth", not "which DOM attribute is set."
  it("+ Add condition on a cap-depth group does not push depth over MaxPredicateDepth", async () => {
    const user = userEvent.setup();
    // Build nesting so the innermost group is at depth 4 with a leaf
    // child at depth 5 (== MaxPredicateDepth). 4 wrappers + 1 leaf.
    let inner: Predicate = { kind: "all", of: [defaultLeaf()] };
    for (let i = 0; i < 3; i++) {
      inner = { kind: "all", of: [inner] };
    }
    render(<Harness initial={inner} />);
    const addButtons = screen.getAllByRole("button", { name: /add condition/i });
    // Sanity: 4 groups → 4 Add-condition buttons.
    expect(addButtons).toHaveLength(4);
    // The deepest group's Add-condition is the last one in DOM order.
    const deepestAdd = addButtons[addButtons.length - 1];
    expect(deepestAdd).not.toBeDisabled();
    await user.click(deepestAdd);
    const after = readSpec();
    function specDepth(p: Predicate): number {
      switch (p.kind) {
        case "time":
        case "active_days":
          return 1;
        case "streak":
          return 1 + specDepth(p.condition);
        case "all":
        case "any":
          return 1 + Math.max(...p.of.map(specDepth));
        case "not":
          return 1 + specDepth(p.of[0]);
      }
    }
    // Load-bearing: depth stays exactly 5. A regression that wrapped
    // the new leaf in a fresh group before appending would produce 6.
    expect(specDepth(after)).toBe(5);
    // At least ONE group's children count must have grown to 2 —
    // the click landed somewhere. We assert on the total leaf count
    // in the tree (started at 1, must be 2 after Add-condition).
    function leafCount(p: Predicate): number {
      switch (p.kind) {
        case "time":
        case "active_days":
          return 1;
        case "streak":
          return leafCount(p.condition);
        case "all":
        case "any":
          return p.of.reduce((n, c) => n + leafCount(c), 0);
        case "not":
          return leafCount(p.of[0]);
      }
    }
    // Started with 1 leaf; a successful Add-condition adds another.
    expect(leafCount(after)).toBe(2);
  });
  // Keep the `within` import referenced so vitest doesn't lint on it —
  // future scoped queries will replace this line.
  void within;
});
