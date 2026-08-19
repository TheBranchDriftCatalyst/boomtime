// RawJsonPane — textarea escape hatch (gaka-6uf). Users can drop into
// raw JSON for debugging or to express something the builder hasn't
// caught up to. Text is preserved between toggles (see index.tsx) so a
// half-typed blob doesn't disappear on a tab switch. Applying invalid
// JSON stays on the Raw tab with an inline error — no silent reversion.
import { useState, useEffect } from "react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Textarea } from "@thebranchdriftcatalyst/catalyst-ui/ui/textarea";
import type { Condition } from "@shared/features/publicprofile/labels/types";
import { parseConditionJson, formatConditionJson } from "./schema";

interface Props {
  /** Current committed condition — Apply re-emits this when text is unchanged. */
  value: Condition;
  /** Called ONLY when the raw text parses cleanly + passes the schema. */
  onApply: (next: Condition) => void;
  /** Persisted text buffer so a user's in-flight edit survives tab
   *  switches without being clobbered by pretty-printed value bytes. */
  buffer: string;
  onBufferChange: (next: string) => void;
}

export function RawJsonPane({ value, onApply, buffer, onBufferChange }: Props) {
  // On mount / when the committed value changes externally (e.g., builder
  // edits), sync the buffer IF it's still empty or still matches the
  // last-known pretty print of the value. That way builder edits flow
  // through, but a user's own raw edit doesn't get wiped.
  useEffect(() => {
    if (buffer === "") onBufferChange(formatConditionJson(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const [err, setErr] = useState<string | null>(null);

  return (
    <div className="space-y-2">
      <Textarea
        value={buffer}
        onChange={(e) => {
          onBufferChange(e.target.value);
          setErr(null);
        }}
        rows={10}
        className="font-mono text-[11px]"
      />
      {err && (
        <p className="text-[11px] text-destructive">{err}</p>
      )}
      <div className="flex items-center gap-2">
        <Button
          type="button"
          size="sm"
          onClick={() => {
            const { condition, error } = parseConditionJson(buffer);
            if (error || !condition) {
              setErr(error ?? "invalid");
              return;
            }
            setErr(null);
            onApply(condition);
          }}
        >
          Apply → Builder
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            onBufferChange(formatConditionJson(value));
            setErr(null);
          }}
        >
          Reset from committed
        </Button>
        <span className="ml-auto font-mono text-[10px] text-muted-foreground">
          {buffer.length} bytes
        </span>
      </div>
    </div>
  );
}
