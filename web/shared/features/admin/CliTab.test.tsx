// CliTab unit tests (admin CLI-runner frontend, BOOM_FEATURE_ADMIN_CLI).
// msw-backed: the spec/run/complete endpoints are stubbed per test; the
// suite covers the disabled state, the per-type field dispatcher, the
// flags-object submit shape (positionals INSIDE flags), the mutating
// dry-run + typed-confirm gate, the cobra-complete combobox contract, the
// client-side enum path, and complete-failure degradation.
import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "@shared/test/msw/server";
import { mockCliRunWs } from "@shared/test/ws";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import type { CliCommandSpec, CliParam, CliRunResponse } from "@shared/lib/api";
import { CliTab } from "./CliTab";

// --- fixtures ----------------------------------------------------------------

const param = (over: Partial<CliParam> & { name: string }): CliParam => ({
  type: "string",
  positional: false,
  required: false,
  secret: false,
  completable: false,
  ...over,
});

const USER_SHOW: CliCommandSpec = {
  command: "user show",
  short: "Show one user",
  classification: "readonly",
  dryRunSupported: false,
  params: [
    param({ name: "username", positional: true, required: true, completable: true }),
  ],
};

const BACKFILL_LAST_CONTEXT: CliCommandSpec = {
  command: "backfill last-context",
  short: "Backfill heartbeat last-context",
  classification: "mutating",
  dryRunSupported: true,
  params: [
    param({
      name: "dry-run",
      type: "bool",
      default: "true",
      usage: "preview without writing",
    }),
  ],
};

const GITHUB_STATS: CliCommandSpec = {
  command: "backfill github-stats",
  short: "Refresh cached GitHub stats",
  classification: "mutating",
  dryRunSupported: false,
  params: [param({ name: "user", completable: true, usage: "single user" })],
};

// Synthetic command exercising every field type in one form.
const KITCHEN_SINK: CliCommandSpec = {
  command: "kitchen sink",
  short: "One of every param type",
  classification: "readonly",
  dryRunSupported: false,
  params: [
    param({ name: "verbose", type: "bool", default: "false" }),
    param({ name: "count", type: "int" }),
    param({ name: "note", usage: "free text" }),
    param({ name: "mode", type: "enum", enum: ["fast", "slow"] }),
    param({ name: "tags", type: "stringSlice" }),
    param({ name: "api-token", secret: true }),
  ],
};

const runResult = (over: Partial<CliRunResponse> = {}): CliRunResponse => ({
  ok: true,
  output: "done",
  exitError: "",
  dryRun: false,
  durationMs: 12,
  ...over,
});

function stubSpec(...commands: CliCommandSpec[]) {
  server.use(
    http.get("/api/v1/admin/cli/spec", () =>
      HttpResponse.json({ commands }),
    ),
  );
}

/** Render the tab and click a command in the left-hand list. */
async function renderAndSelect(command: string) {
  const utils = renderWithProviders(<CliTab />);
  await userEvent.click(await screen.findByText(command));
  return utils;
}

// --- tests -------------------------------------------------------------------

describe("CliTab — disabled state", () => {
  it("renders the friendly disabled card when the spec 404s", async () => {
    server.use(
      http.get("/api/v1/admin/cli/spec", () =>
        HttpResponse.json({ message: "not found" }, { status: 404 }),
      ),
    );
    renderWithProviders(<CliTab />);
    expect(
      await screen.findByText(/CLI runner is disabled/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/BOOM_FEATURE_ADMIN_CLI=on/)).toBeInTheDocument();
    // No command list / run affordances in the disabled state.
    expect(screen.queryByRole("button", { name: /run/i })).not.toBeInTheDocument();
  });

  it("renders a plain error (not the disabled card) on a non-404 failure", async () => {
    server.use(
      http.get("/api/v1/admin/cli/spec", () =>
        HttpResponse.json({ message: "boom" }, { status: 500 }),
      ),
    );
    renderWithProviders(<CliTab />);
    expect(
      await screen.findByText(/failed to load command catalog/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/CLI runner is disabled/i)).not.toBeInTheDocument();
  });
});

describe("CliTab — typed form fields", () => {
  it("renders the right control per param type", async () => {
    stubSpec(KITCHEN_SINK);
    await renderAndSelect("kitchen sink");

    // bool → checkbox
    expect(
      screen.getByRole("checkbox", { name: /--verbose/ }),
    ).toBeInTheDocument();
    // int → number input
    expect(
      screen.getByRole("spinbutton", { name: /--count/ }),
    ).toBeInTheDocument();
    // plain string → text input
    expect(screen.getByLabelText(/--note/)).toHaveAttribute("type", "text");
    // enum → select (Radix trigger has role=combobox), NOT a completable input
    expect(
      screen.getByRole("combobox", { name: /--mode/ }),
    ).toBeInTheDocument();
    // stringSlice → tag input
    expect(screen.getByLabelText(/--tags/)).toHaveAttribute(
      "placeholder",
      "Type a value, press Enter",
    );
    // secret → password input, never pre-filled
    const secret = screen.getByLabelText(/--api-token/);
    expect(secret).toHaveAttribute("type", "password");
    expect(secret).toHaveValue("");
    // usage text renders as help
    expect(screen.getByText("free text")).toBeInTheDocument();
  });

  it("rejects a non-integer int before submit (no network call)", async () => {
    stubSpec(KITCHEN_SINK);
    let ran = false;
    server.use(
      http.post("/api/v1/admin/cli/run", () => {
        ran = true;
        return HttpResponse.json(runResult());
      }),
    );
    await renderAndSelect("kitchen sink");

    await userEvent.type(
      screen.getByRole("spinbutton", { name: /--count/ }),
      "1.5",
    );
    await userEvent.click(screen.getByRole("button", { name: /^Run$/ }));
    expect(await screen.findByText(/must be an integer/i)).toBeInTheDocument();
    expect(ran).toBe(false);
  });

  it("submits every param INSIDE flags keyed by name, with typed coercions", async () => {
    stubSpec(KITCHEN_SINK);
    const ws = mockCliRunWs();
    try {
      await renderAndSelect("kitchen sink");

      await userEvent.click(screen.getByRole("checkbox", { name: /--verbose/ }));
      await userEvent.type(
        screen.getByRole("spinbutton", { name: /--count/ }),
        "5",
      );
      await userEvent.type(screen.getByLabelText(/--note/), "hi");
      // tags: two chips via Enter
      const tags = screen.getByLabelText(/--tags/);
      await userEvent.type(tags, "a{Enter}b{Enter}");
      expect(screen.getByText("a")).toBeInTheDocument();
      expect(screen.getByText("b")).toBeInTheDocument();

      await userEvent.click(screen.getByRole("button", { name: /^Run$/ }));
      const req = await ws.requestReceived();
      expect(req).toEqual({
        command: "kitchen sink",
        // int coerced to number, slice to array, empty optionals omitted,
        // bools explicit.
        flags: { verbose: true, count: 5, note: "hi", tags: ["a", "b"] },
      });
      // readonly run → no confirm key at all
      expect(req).not.toHaveProperty("confirm");

      // Streamed output tails into the terminal viewer.
      ws.send({ type: "output", data: "listing users…" });
      ws.send({ type: "done", durationMs: 12 });
      expect(await screen.findByText(/done/)).toBeInTheDocument();
    } finally {
      ws.stop();
    }
  });

  it("submits a positional param inside flags keyed by its name", async () => {
    stubSpec(USER_SHOW);
    const ws = mockCliRunWs();
    // Typing into the completable field fires debounced completes.
    server.use(
      http.post("/api/v1/admin/cli/complete", () =>
        HttpResponse.json({
          suggestions: [],
          directive: {
            noFileComp: true,
            noSpace: false,
            noSort: false,
            keepOrder: false,
            error: false,
          },
        }),
      ),
    );
    try {
      await renderAndSelect("user show");

      await userEvent.type(
        screen.getByRole("combobox", { name: /username/ }),
        "panda",
      );
      await userEvent.click(screen.getByRole("button", { name: /^Run$/ }));
      const req = await ws.requestReceived();
      expect(req).toEqual({
        command: "user show",
        flags: { username: "panda" },
      });
    } finally {
      ws.stop();
    }
  });

  it("blocks submit when a required positional is empty", async () => {
    stubSpec(USER_SHOW);
    let ran = false;
    server.use(
      http.post("/api/v1/admin/cli/run", () => {
        ran = true;
        return HttpResponse.json(runResult());
      }),
    );
    await renderAndSelect("user show");
    await userEvent.click(screen.getByRole("button", { name: /^Run$/ }));
    expect(await screen.findByText("Required")).toBeInTheDocument();
    expect(ran).toBe(false);
  });
});

describe("CliTab — mutating dry-run + confirm gate", () => {
  it("defaults dry-run ON and omits the dry-run key on a preview run", async () => {
    stubSpec(BACKFILL_LAST_CONTEXT);
    const ws = mockCliRunWs();
    try {
      await renderAndSelect("backfill last-context");

      const toggle = screen.getByRole("switch", { name: /dry run/i });
      expect(toggle).toBeChecked();

      await userEvent.click(
        screen.getByRole("button", { name: /run \(dry-run\)/i }),
      );
      const req = await ws.requestReceived();
      // No dry-run key sent — the backend defaults it true — and no confirm.
      expect(req.flags).toEqual({});
      expect(req).not.toHaveProperty("confirm");

      // The start frame's dryRun flows to the viewer's badge.
      ws.send({ type: "start", dryRun: true });
      ws.send({ type: "output", data: "would write 3 rows" });
      ws.send({ type: "done", durationMs: 5 });
      expect(await screen.findByText("dry-run")).toBeInTheDocument();
    } finally {
      ws.stop();
    }
  });

  it("applying requires typing the exact command, then sends dry-run:false + confirm", async () => {
    stubSpec(BACKFILL_LAST_CONTEXT);
    const ws = mockCliRunWs();
    try {
      await renderAndSelect("backfill last-context");

      // Flip dry-run OFF → the action becomes Apply… and opens the gate.
      await userEvent.click(screen.getByRole("switch", { name: /dry run/i }));
      await userEvent.click(screen.getByRole("button", { name: /apply…/i }));

      await screen.findByRole("dialog");
      const applyBtn = screen.getByRole("button", { name: /^Apply$/ });
      expect(applyBtn).toBeDisabled();

      // A wrong sentinel keeps the gate shut.
      const input = screen.getByPlaceholderText("backfill last-context");
      await userEvent.type(input, "nope");
      expect(applyBtn).toBeDisabled();

      await userEvent.clear(input);
      await userEvent.type(input, "backfill last-context");
      expect(applyBtn).toBeEnabled();
      await userEvent.click(applyBtn);

      const req = await ws.requestReceived();
      expect(req).toEqual({
        command: "backfill last-context",
        flags: { "dry-run": false },
        confirm: "backfill last-context",
      });
    } finally {
      ws.stop();
    }
  });

  it("a mutating command WITHOUT dry-run support gates every run behind confirm", async () => {
    stubSpec(GITHUB_STATS);
    const ws = mockCliRunWs();
    server.use(
      http.post("/api/v1/admin/cli/complete", () =>
        HttpResponse.json({
          suggestions: [],
          directive: {
            noFileComp: true,
            noSpace: false,
            noSort: false,
            keepOrder: false,
            error: false,
          },
        }),
      ),
    );
    try {
      await renderAndSelect("backfill github-stats");

      // No dry-run toggle: the command doesn't support it.
      expect(screen.queryByRole("switch")).not.toBeInTheDocument();

      // The Run button IS the apply → confirm dialog.
      await userEvent.click(screen.getByRole("button", { name: /apply…/i }));
      const input = await screen.findByPlaceholderText("backfill github-stats");
      await userEvent.type(input, "backfill github-stats");
      await userEvent.click(screen.getByRole("button", { name: /^Apply$/ }));

      const req = await ws.requestReceived();
      expect(req).toEqual({
        command: "backfill github-stats",
        flags: {},
        confirm: "backfill github-stats",
      });
    } finally {
      ws.stop();
    }
  });
});

describe("CliTab — autocomplete", () => {
  it("calls completeCli with {command,args,flag?,toComplete} for a positional and renders suggestions", async () => {
    stubSpec(USER_SHOW);
    let completeBody: Record<string, unknown> | null = null;
    server.use(
      http.post("/api/v1/admin/cli/complete", async ({ request }) => {
        completeBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({
          suggestions: [
            { value: "panda", description: "admin" },
            { value: "pangolin" },
          ],
          directive: {
            noFileComp: true,
            noSpace: false,
            noSort: false,
            keepOrder: false,
            error: false,
          },
        });
      }),
    );
    await renderAndSelect("user show");

    await userEvent.type(
      screen.getByRole("combobox", { name: /username/ }),
      "pa",
    );

    // Suggestions render (value + optional description) after the debounce.
    expect(await screen.findByText("panda")).toBeInTheDocument();
    expect(screen.getByText("admin")).toBeInTheDocument();
    expect(screen.getByText("pangolin")).toBeInTheDocument();

    // Positional: flag omitted, args = prior positional values (none here).
    expect(completeBody).toMatchObject({
      command: "user show",
      args: [],
      toComplete: "pa",
    });
    expect(completeBody).not.toHaveProperty("flag");

    // Clicking a suggestion fills the input.
    await userEvent.click(screen.getByRole("option", { name: /panda admin/ }));
    expect(screen.getByRole("combobox", { name: /username/ })).toHaveValue(
      "panda",
    );
  });

  it("names the flag when completing a flag param", async () => {
    stubSpec(GITHUB_STATS);
    let completeBody: Record<string, unknown> | null = null;
    server.use(
      http.post("/api/v1/admin/cli/complete", async ({ request }) => {
        completeBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({
          suggestions: [{ value: "panda" }],
          directive: {
            noFileComp: true,
            noSpace: false,
            noSort: false,
            keepOrder: false,
            error: false,
          },
        });
      }),
    );
    await renderAndSelect("backfill github-stats");

    await userEvent.type(screen.getByRole("combobox", { name: /--user/ }), "p");
    expect(await screen.findByText("panda")).toBeInTheDocument();
    expect(completeBody).toMatchObject({
      command: "backfill github-stats",
      args: [],
      flag: "user",
      toComplete: "p",
    });
  });

  it("shows a muted 'No matches' on the error directive", async () => {
    stubSpec(USER_SHOW);
    server.use(
      http.post("/api/v1/admin/cli/complete", () =>
        HttpResponse.json({
          suggestions: [],
          directive: {
            noFileComp: true,
            noSpace: false,
            noSort: false,
            keepOrder: false,
            error: true,
          },
        }),
      ),
    );
    await renderAndSelect("user show");
    await userEvent.type(
      screen.getByRole("combobox", { name: /username/ }),
      "zz",
    );
    expect(await screen.findByText(/no matches/i)).toBeInTheDocument();
  });

  it("enum params complete client-side without calling the endpoint", async () => {
    stubSpec(KITCHEN_SINK);
    let completeCalls = 0;
    server.use(
      http.post("/api/v1/admin/cli/complete", () => {
        completeCalls++;
        return HttpResponse.json({
          suggestions: [],
          directive: {
            noFileComp: true,
            noSpace: false,
            noSort: false,
            keepOrder: false,
            error: false,
          },
        });
      }),
    );
    await renderAndSelect("kitchen sink");

    await userEvent.click(screen.getByRole("combobox", { name: /--mode/ }));
    // Options come straight from param.enum.
    expect(await screen.findByRole("option", { name: "fast" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "slow" })).toBeInTheDocument();
    expect(completeCalls).toBe(0);
  });

  it("degrades to a plain text input when the complete endpoint fails", async () => {
    stubSpec(USER_SHOW);
    server.use(
      http.post("/api/v1/admin/cli/complete", () =>
        HttpResponse.json({ message: "boom" }, { status: 500 }),
      ),
    );
    await renderAndSelect("user show");

    const input = screen.getByRole("combobox", { name: /username/ });
    await userEvent.type(input, "manual-entry");
    // Manual typing still works and no listbox ever opens.
    expect(input).toHaveValue("manual-entry");
    // Give the debounce + failed fetch a beat, then confirm no dropdown.
    await waitFor(() =>
      expect(screen.queryByRole("listbox")).not.toBeInTheDocument(),
    );
  });
});
