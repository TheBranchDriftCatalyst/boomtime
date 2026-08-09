// CliTab — Admin > Commands (BOOM_FEATURE_ADMIN_CLI). Browser console for the
// curated CLI-runner backend: lists every web-annotated command from
// GET /admin/cli/spec, renders a typed form per param, offers cobra-powered
// autocomplete on completable params, and runs commands in-process showing
// the captured output.
//
// Safety UX mirrors the backend contract (internal/admin/cli_run.go):
//   - readonly commands run with a plain Run button.
//   - mutating + dryRunSupported defaults the Dry run toggle ON; omitting the
//     "dry-run" key IS a dry-run server-side. Applying flips dry-run:false
//     AND requires typing the command string into a confirm dialog (the
//     confirm sentinel the backend enforces).
//   - mutating WITHOUT dry-run support: every run is an apply → confirm gate.
//   - destructive never reaches the web (backend refuses), but if one ever
//     appeared in the spec it would hit the same typed-confirm gate.
//
// A 404 from the spec endpoint means the backend feature flag is off — the
// tab renders a friendly disabled card, not an error.
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Loader2, Play, TerminalSquare, TriangleAlert, X } from "lucide-react";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Checkbox } from "@thebranchdriftcatalyst/catalyst-ui/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/select";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { Switch } from "@thebranchdriftcatalyst/catalyst-ui/ui/switch";
import { api, ApiError } from "@/lib/api";
import type {
  CliClassification,
  CliCommandSpec,
  CliParam,
  CliRunResponse,
} from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

// The backend binder defaults this flag TRUE when absent; the tab renders it
// as the dedicated Dry run toggle instead of a generic checkbox.
const DRY_RUN_FLAG = "dry-run";

const CLASS_STYLES: Record<CliClassification, string> = {
  readonly: "border-emerald-500/40 bg-emerald-500/15 text-emerald-400",
  mutating: "border-amber-500/40 bg-amber-500/15 text-amber-400",
  destructive: "border-destructive/40 bg-destructive/15 text-destructive",
};

function ClassBadge({ classification }: { classification: CliClassification }) {
  return (
    <Badge
      variant="outline"
      className={cn(
        "px-1.5 py-0 text-[10px] font-semibold uppercase tracking-wider",
        CLASS_STYLES[classification],
      )}
    >
      {classification}
    </Badge>
  );
}

// --- form value plumbing -----------------------------------------------------

type ParamValue = string | boolean | string[];

// Initial form value per param. Secrets are NEVER pre-filled (not even a
// default); pflag's stringSlice DefValue is the noise string "[]" so slices
// always start empty.
function initialValue(p: CliParam): ParamValue {
  switch (p.type) {
    case "bool":
      return p.default === "true";
    case "stringSlice":
      return [];
    default:
      return p.secret ? "" : (p.default ?? "");
  }
}

function initialValues(spec: CliCommandSpec): Record<string, ParamValue> {
  const out: Record<string, ParamValue> = {};
  for (const p of spec.params) out[p.name] = initialValue(p);
  return out;
}

// Params the generic form renders. The dry-run flag is owned by the dedicated
// toggle when the command supports dry-run.
function formParams(spec: CliCommandSpec): CliParam[] {
  return spec.params.filter(
    (p) => !(spec.dryRunSupported && p.name === DRY_RUN_FLAG),
  );
}

// Validate + assemble the run request's `flags` object. Positionals and flags
// both go in keyed by param name (the backend binder routes positionals by
// the spec's positional marker). Empty optional values are omitted so the
// binder's defaults apply.
function buildFlags(
  spec: CliCommandSpec,
  values: Record<string, ParamValue>,
): { flags: Record<string, unknown>; errors: Record<string, string> } {
  const flags: Record<string, unknown> = {};
  const errors: Record<string, string> = {};
  for (const p of formParams(spec)) {
    const v = values[p.name];
    switch (p.type) {
      case "bool":
        flags[p.name] = v === true;
        break;
      case "int": {
        const s = typeof v === "string" ? v.trim() : "";
        if (s === "") {
          if (p.required) errors[p.name] = "Required";
          break;
        }
        const n = Number(s);
        if (!Number.isInteger(n)) {
          errors[p.name] = "Must be an integer";
          break;
        }
        flags[p.name] = n;
        break;
      }
      case "stringSlice": {
        const arr = Array.isArray(v) ? v : [];
        if (arr.length === 0) {
          if (p.required) errors[p.name] = "Required";
          break;
        }
        flags[p.name] = arr;
        break;
      }
      default: {
        // string | enum
        const s = typeof v === "string" ? v : "";
        if (s === "") {
          if (p.required) errors[p.name] = "Required";
          break;
        }
        flags[p.name] = s;
        break;
      }
    }
  }
  return { flags, errors };
}

// Values of the POSITIONAL params that precede `until` in declared order —
// the `args` array contextual completers receive. For a flag (until
// undefined) every positional value is prior context.
function priorPositionalValues(
  spec: CliCommandSpec,
  values: Record<string, ParamValue>,
  until?: string,
): string[] {
  const out: string[] = [];
  for (const p of spec.params) {
    if (!p.positional) continue;
    if (until !== undefined && p.name === until) break;
    const v = values[p.name];
    out.push(typeof v === "string" ? v : "");
  }
  return out;
}

// --- autocomplete combobox ---------------------------------------------------

interface CompletableInputProps {
  id: string;
  spec: CliCommandSpec;
  param: CliParam;
  value: string;
  values: Record<string, ParamValue>;
  onChange: (v: string) => void;
  disabled?: boolean;
}

/**
 * Debounced (~200ms) combobox over POST /admin/cli/complete. Degrades to a
 * plain text input on any failure — suggestions are a convenience, never a
 * gate on manual entry. Server order is preserved as-is (satisfies the
 * noSort/keepOrder directives); the error directive renders a muted
 * "no matches" row.
 */
function CompletableInput({
  id,
  spec,
  param,
  value,
  values,
  onChange,
  disabled,
}: CompletableInputProps) {
  const [open, setOpen] = useState(false);
  const [suggestions, setSuggestions] = useState<
    { value: string; description?: string }[]
  >([]);
  const [errorDirective, setErrorDirective] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Guards against out-of-order responses (fast typing).
  const seq = useRef(0);

  const fetchSuggestions = (toComplete: string) => {
    const mySeq = ++seq.current;
    api
      .completeCli({
        command: spec.command,
        args: priorPositionalValues(
          spec,
          values,
          param.positional ? param.name : undefined,
        ),
        flag: param.positional ? undefined : param.name,
        toComplete,
      })
      .then((res) => {
        if (mySeq !== seq.current) return;
        setSuggestions(res.suggestions ?? []);
        setErrorDirective(res.directive?.error ?? false);
        setOpen(true);
      })
      .catch(() => {
        // Complete endpoint down / feature off mid-session: plain input.
        if (mySeq !== seq.current) return;
        setSuggestions([]);
        setErrorDirective(false);
        setOpen(false);
      });
  };

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);

  const schedule = (toComplete: string) => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => fetchSuggestions(toComplete), 200);
  };

  const visible = open && (suggestions.length > 0 || errorDirective);

  return (
    <div className="relative">
      <Input
        id={id}
        role="combobox"
        aria-expanded={visible}
        aria-autocomplete="list"
        aria-controls={`${id}-listbox`}
        autoComplete="off"
        value={value}
        disabled={disabled}
        onChange={(e) => {
          onChange(e.target.value);
          schedule(e.target.value);
        }}
        onFocus={() => schedule(value)}
        onBlur={() => {
          // Delay so an option mousedown wins over the blur-close.
          setTimeout(() => setOpen(false), 100);
        }}
        onKeyDown={(e) => {
          if (e.key === "Escape") setOpen(false);
        }}
      />
      {visible && (
        <ul
          id={`${id}-listbox`}
          role="listbox"
          className="absolute z-20 mt-1 max-h-56 w-full overflow-auto rounded-md border border-border bg-popover py-1 text-sm shadow-md"
        >
          {errorDirective || suggestions.length === 0 ? (
            <li className="px-3 py-1.5 text-xs text-muted-foreground">
              No matches
            </li>
          ) : (
            suggestions.map((s) => (
              <li
                key={s.value}
                role="option"
                aria-selected={s.value === value}
                className="cursor-pointer px-3 py-1.5 hover:bg-accent"
                // mousedown fires before the input's blur, so selection wins.
                onMouseDown={(e) => {
                  e.preventDefault();
                  onChange(s.value);
                  setOpen(false);
                }}
              >
                <span className="font-mono">{s.value}</span>
                {s.description && (
                  <span className="ml-2 text-xs text-muted-foreground">
                    {s.description}
                  </span>
                )}
              </li>
            ))
          )}
        </ul>
      )}
    </div>
  );
}

// --- stringSlice tag input ---------------------------------------------------

function TagsInput({
  id,
  value,
  onChange,
  disabled,
}: {
  id: string;
  value: string[];
  onChange: (v: string[]) => void;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState("");

  const commit = () => {
    const t = draft.trim();
    if (t && !value.includes(t)) onChange([...value, t]);
    setDraft("");
  };

  return (
    <div>
      {value.length > 0 && (
        <div className="mb-1.5 flex flex-wrap gap-1.5">
          {value.map((tag) => (
            <span
              key={tag}
              className="inline-flex items-center gap-1 rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-xs"
            >
              {tag}
              <button
                type="button"
                aria-label={`Remove ${tag}`}
                className="text-muted-foreground hover:text-foreground"
                disabled={disabled}
                onClick={() => onChange(value.filter((t) => t !== tag))}
              >
                <X className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      )}
      <Input
        id={id}
        value={draft}
        disabled={disabled}
        autoComplete="off"
        placeholder="Type a value, press Enter"
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            commit();
          } else if (e.key === "Backspace" && draft === "" && value.length) {
            onChange(value.slice(0, -1));
          }
        }}
        onBlur={commit}
      />
    </div>
  );
}

// --- per-param field dispatcher ----------------------------------------------

interface ParamFieldProps {
  spec: CliCommandSpec;
  param: CliParam;
  value: ParamValue;
  values: Record<string, ParamValue>;
  error?: string;
  onChange: (v: ParamValue) => void;
  disabled?: boolean;
}

function ParamField({
  spec,
  param,
  value,
  values,
  error,
  onChange,
  disabled,
}: ParamFieldProps) {
  const id = useId();

  const label = (
    <Label htmlFor={id} className="font-mono text-xs">
      {param.positional ? param.name : `--${param.name}`}
      {param.required && (
        <span aria-hidden className="ml-0.5 text-destructive">
          *
        </span>
      )}
    </Label>
  );

  let control: ReactNode;
  switch (param.type) {
    case "bool":
      control = (
        <div className="flex items-center gap-2">
          <Checkbox
            id={id}
            checked={value === true}
            onCheckedChange={(c) => onChange(c === true)}
            disabled={disabled}
          />
          {label}
        </div>
      );
      break;
    case "enum":
      // enum completes CLIENT-SIDE from the spec — never the endpoint.
      control = (
        <Select
          value={typeof value === "string" ? value : ""}
          onValueChange={(v: string) => onChange(v)}
          disabled={disabled}
        >
          <SelectTrigger id={id} className="h-8 text-xs">
            <SelectValue placeholder="Select…" />
          </SelectTrigger>
          <SelectContent>
            {(param.enum ?? []).map((opt) => (
              <SelectItem key={opt} value={opt}>
                {opt}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      );
      break;
    case "int":
      control = (
        <Input
          id={id}
          type="number"
          inputMode="numeric"
          step={1}
          autoComplete="off"
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          aria-invalid={!!error}
        />
      );
      break;
    case "stringSlice":
      control = (
        <TagsInput
          id={id}
          value={Array.isArray(value) ? value : []}
          onChange={onChange}
          disabled={disabled}
        />
      );
      break;
    default:
      // string. Secret ⇒ password input, never pre-filled (initialValue
      // guarantees the empty start). Completable ⇒ debounced combobox.
      if (param.secret) {
        control = (
          <Input
            id={id}
            type="password"
            autoComplete="new-password"
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
            aria-invalid={!!error}
          />
        );
      } else if (param.completable) {
        control = (
          <CompletableInput
            id={id}
            spec={spec}
            param={param}
            value={typeof value === "string" ? value : ""}
            values={values}
            onChange={onChange}
            disabled={disabled}
          />
        );
      } else {
        control = (
          <Input
            id={id}
            type="text"
            autoComplete="off"
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
            aria-invalid={!!error}
          />
        );
      }
  }

  return (
    <div className="space-y-1">
      {param.type !== "bool" && label}
      {control}
      {param.usage && (
        <p className="text-[11px] text-muted-foreground">{param.usage}</p>
      )}
      {error && <p className="text-[11px] text-destructive">{error}</p>}
    </div>
  );
}

// --- output panel ------------------------------------------------------------

function OutputPanel({ result }: { result: CliRunResponse }) {
  return (
    <div className="space-y-2" data-testid="cli-output-panel">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <Badge
          variant="outline"
          className={cn(
            "px-1.5 py-0 text-[10px] font-semibold uppercase tracking-wider",
            result.ok
              ? "border-emerald-500/40 bg-emerald-500/15 text-emerald-400"
              : "border-destructive/40 bg-destructive/15 text-destructive",
          )}
        >
          {result.ok ? "ok" : "failed"}
        </Badge>
        <Badge
          variant="outline"
          className={cn(
            "px-1.5 py-0 text-[10px] font-semibold uppercase tracking-wider",
            result.dryRun
              ? "border-sky-500/40 bg-sky-500/15 text-sky-400"
              : "border-amber-500/40 bg-amber-500/15 text-amber-400",
          )}
        >
          {result.dryRun ? "dry-run" : "applied"}
        </Badge>
        <span className="text-muted-foreground">{result.durationMs} ms</span>
      </div>
      {result.output !== "" && (
        <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded border border-border bg-secondary/60 p-3 font-mono text-[11px] leading-relaxed">
          {result.output}
        </pre>
      )}
      {result.exitError !== "" && (
        <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded border border-destructive/40 bg-destructive/5 p-3 font-mono text-[11px] leading-relaxed text-destructive">
          {result.exitError}
        </pre>
      )}
      {result.output === "" && result.exitError === "" && (
        <p className="text-xs text-muted-foreground">(no output)</p>
      )}
    </div>
  );
}

// --- per-command run panel ---------------------------------------------------

function CommandPanel({ spec }: { spec: CliCommandSpec }) {
  const [values, setValues] = useState<Record<string, ParamValue>>(() =>
    initialValues(spec),
  );
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  // Dry run defaults ON for every mutating command that supports it.
  const [dryRun, setDryRun] = useState(true);
  // Typed-confirm dialog state (apply gate).
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [typedConfirm, setTypedConfirm] = useState("");
  const dryRunToggleId = useId();
  const confirmInputId = useId();

  // No reset-on-command-change effect needed: CliTab renders this panel with
  // key={spec.command}, so switching commands remounts with fresh state.
  const run = useMutation({
    mutationFn: (body: {
      command: string;
      flags: Record<string, unknown>;
      confirm?: string;
    }) => api.runCliCommand(body),
  });

  const showDryRunToggle =
    spec.classification !== "readonly" && spec.dryRunSupported;
  // Every run of a non-readonly command that ISN'T a dry-run is an apply and
  // must pass the typed-confirm gate. Covers mutating-without-dry-run and the
  // (never-served) destructive class alike.
  const applying =
    spec.classification !== "readonly" && (!spec.dryRunSupported || !dryRun);

  const validate = (): Record<string, unknown> | null => {
    const { flags, errors } = buildFlags(spec, values);
    setFieldErrors(errors);
    return Object.keys(errors).length > 0 ? null : flags;
  };

  const submit = () => {
    const flags = validate();
    if (!flags) return;
    if (applying) {
      // Confirm dialog collects the typed sentinel, then fires the apply.
      setTypedConfirm("");
      setConfirmOpen(true);
      return;
    }
    // Dry-run / readonly path: no dry-run key sent — the backend binder
    // defaults it true for dry-run-supporting commands.
    run.mutate({ command: spec.command, flags });
  };

  const confirmApply = () => {
    const flags = validate();
    if (!flags) return;
    if (spec.dryRunSupported) flags[DRY_RUN_FLAG] = false;
    run.mutate(
      { command: spec.command, flags, confirm: spec.command },
      { onSettled: () => setConfirmOpen(false) },
    );
  };

  const params = formParams(spec);

  return (
    <div className="space-y-4">
      <div>
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="font-mono text-sm font-semibold">{spec.command}</h3>
          <ClassBadge classification={spec.classification} />
        </div>
        <p className="mt-1 text-sm text-muted-foreground">{spec.short}</p>
        {spec.long && spec.long !== spec.short && (
          <p className="mt-1 whitespace-pre-wrap text-xs text-muted-foreground/80">
            {spec.long}
          </p>
        )}
      </div>

      {params.length > 0 && (
        <div className="space-y-3">
          {params.map((p) => (
            <ParamField
              key={p.name}
              spec={spec}
              param={p}
              value={values[p.name]}
              values={values}
              error={fieldErrors[p.name]}
              onChange={(v) =>
                setValues((prev) => ({ ...prev, [p.name]: v }))
              }
              disabled={run.isPending}
            />
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3">
        {showDryRunToggle && (
          <div className="flex items-center gap-2">
            <Switch
              id={dryRunToggleId}
              checked={dryRun}
              onCheckedChange={setDryRun}
              disabled={run.isPending}
            />
            <Label htmlFor={dryRunToggleId} className="text-xs">
              Dry run
            </Label>
          </div>
        )}
        <Button
          size="sm"
          variant={applying ? "destructive" : "default"}
          disabled={run.isPending}
          onClick={submit}
        >
          {run.isPending ? (
            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
          ) : (
            <Play className="mr-1.5 h-3.5 w-3.5" />
          )}
          {applying ? "Apply…" : showDryRunToggle ? "Run (dry-run)" : "Run"}
        </Button>
      </div>

      {run.isError && (
        <p className="text-sm text-destructive">
          Run failed:{" "}
          {run.error instanceof ApiError
            ? run.error.message
            : "request error"}
        </p>
      )}
      {run.data && <OutputPanel result={run.data} />}

      {/* Typed-confirm apply gate. The backend independently enforces
          confirm === command; this dialog is the human-side mirror. */}
      <Dialog
        open={confirmOpen}
        onOpenChange={(o) => {
          if (!o && !run.isPending) setConfirmOpen(false);
        }}
      >
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle className="font-mono text-base">
              <TriangleAlert className="mr-2 inline h-4 w-4 text-destructive" />
              Apply {spec.command}
            </DialogTitle>
            <DialogDescription>
              This runs the command for real{" "}
              {spec.dryRunSupported
                ? "(dry-run off) "
                : ""}
              and mutates data. Type the command to confirm.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-1.5">
            <Label htmlFor={confirmInputId} className="text-xs">
              Type <span className="font-mono">{spec.command}</span> to confirm
            </Label>
            <Input
              id={confirmInputId}
              value={typedConfirm}
              onChange={(e) => setTypedConfirm(e.target.value)}
              placeholder={spec.command}
              autoComplete="off"
              disabled={run.isPending}
            />
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setConfirmOpen(false)}
              disabled={run.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={typedConfirm !== spec.command || run.isPending}
              onClick={confirmApply}
            >
              {run.isPending ? "Applying…" : "Apply"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// --- tab shell ---------------------------------------------------------------

// Group order for the command list — readonly first (safe to poke), then the
// scarier classes.
const CLASS_ORDER: CliClassification[] = [
  "readonly",
  "mutating",
  "destructive",
];

export function CliTab() {
  const specQuery = useQuery({
    queryKey: qk.adminCliSpec(),
    queryFn: () => api.getCliSpec(),
    staleTime: 5 * 60_000,
    retry: false,
  });
  const [selected, setSelected] = useState<string | null>(null);

  const commands = useMemo(
    () => specQuery.data?.commands ?? [],
    [specQuery.data],
  );
  const grouped = useMemo(() => {
    const byClass = new Map<CliClassification, CliCommandSpec[]>();
    for (const c of commands) {
      const list = byClass.get(c.classification) ?? [];
      list.push(c);
      byClass.set(c.classification, list);
    }
    return CLASS_ORDER.filter((cl) => byClass.has(cl)).map((cl) => ({
      classification: cl,
      commands: byClass.get(cl)!,
    }));
  }, [commands]);

  const selectedSpec = commands.find((c) => c.command === selected) ?? null;

  if (specQuery.isLoading) {
    return (
      <div className="flex h-[40vh] items-center justify-center">
        <Spinner />
      </div>
    );
  }

  // 404 = the routes aren't registered = the backend feature flag is off.
  if (
    specQuery.error instanceof ApiError &&
    specQuery.error.status === 404
  ) {
    return (
      <div className="max-w-2xl rounded-lg border border-dashed border-border p-6">
        <div className="flex items-start gap-3">
          <TerminalSquare className="mt-0.5 h-5 w-5 shrink-0 text-muted-foreground" />
          <div>
            <h2 className="text-sm font-semibold">CLI runner is disabled</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Set{" "}
              <code className="rounded bg-muted/60 px-1 font-mono text-xs">
                BOOM_FEATURE_ADMIN_CLI=on
              </code>{" "}
              in the server environment and restart boomtime to enable the
              admin command runner.
            </p>
          </div>
        </div>
      </div>
    );
  }

  if (specQuery.error || !specQuery.data) {
    return (
      <p className="text-sm text-destructive">Failed to load command catalog.</p>
    );
  }

  return (
    <div className="max-w-6xl">
      <div className="grid gap-6 md:grid-cols-[260px_1fr]">
        {/* Command list */}
        <nav aria-label="Commands" className="space-y-4">
          {commands.length === 0 && (
            <p className="text-sm text-muted-foreground">
              No commands are exposed to the web runner.
            </p>
          )}
          {grouped.map((group) => (
            <div key={group.classification}>
              <p className="mb-1.5 font-mono text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
                {group.classification}
              </p>
              <ul className="space-y-1">
                {group.commands.map((c) => (
                  <li key={c.command}>
                    <button
                      type="button"
                      onClick={() => setSelected(c.command)}
                      className={cn(
                        "w-full rounded border px-2.5 py-1.5 text-left transition-colors",
                        c.command === selected
                          ? "border-primary/50 bg-primary/10"
                          : "border-border hover:bg-muted/40",
                      )}
                    >
                      <span className="block font-mono text-xs font-medium">
                        {c.command}
                      </span>
                      <span className="block truncate text-[11px] text-muted-foreground">
                        {c.short}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>

        {/* Run panel */}
        <section className="min-w-0 rounded-lg border border-border p-4">
          {selectedSpec ? (
            // Keyed remount so per-command state never bleeds across
            // selections (belt to CommandPanel's reset-effect braces).
            <CommandPanel key={selectedSpec.command} spec={selectedSpec} />
          ) : (
            <div className="flex h-40 items-center justify-center text-sm text-muted-foreground">
              Select a command to run.
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
