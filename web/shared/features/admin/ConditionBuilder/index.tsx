// ConditionBuilder — top-level entry point for the smart label condition
// form (gaka-6uf). Renders one of {PrimitiveEditor, ComposerEditor} based
// on value.kind and cascades onChange upward. Also drives a kind picker
// that lets the user swap between the 10 primitives + 3 composers, and
// hosts the tabbed Builder | Raw-JSON layout with a live human preview.
//
// State pattern is lift-and-lower via `{value, onChange}`; every edit
// produces a fresh Condition (structuredClone-style — the child components
// return a NEW value they never mutate). Same pattern as
// features/goals/PredicateBuilder. Depth propagates via a prop so the
// composer's add-child affordance can disable itself at MAX_CONDITION_DEPTH.
import { useState } from "react";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import type { Condition } from "@shared/features/publicprofile/labels/types";
import { formatCondition } from "@shared/features/publicprofile/labels/formatCondition";
import { PrimitiveEditor } from "./PrimitiveEditor";
import { ComposerEditor } from "./ComposerEditor";
import { RawJsonPane } from "./RawJsonPane";
import {
  COMPOSER_KINDS,
  KIND_LABELS,
  PRIMITIVE_KINDS,
  newCondition,
  type ComposerKind,
  type PrimitiveKind,
} from "./schema";

interface Props {
  value: Condition;
  onChange: (next: Condition) => void;
  /** Composer-nesting depth this instance sits at (0 = root). Passed
   *  through by ComposerEditor when it recurses. */
  depth?: number;
}

const COMPOSER_SET: ReadonlySet<string> = new Set(COMPOSER_KINDS);

/** Recursive dispatcher: renders the primitive editor OR the composer
 *  editor. The composer editor calls back into this component per child. */
export function ConditionBuilder({ value, onChange, depth = 0 }: Props) {
  const isRoot = depth === 0;

  // Root-only chrome: kind picker + tabbed Raw pane. Nested nodes stay compact.
  const editor =
    value.kind === "all" || value.kind === "any" || value.kind === "not" ? (
      <ComposerEditor value={value} onChange={onChange} depth={depth} />
    ) : (
      <PrimitiveEditor value={value} onChange={onChange} />
    );

  if (!isRoot) {
    // Non-root nodes render only the type picker + editor to save vertical
    // space inside a composer card. The parent composer supplies the
    // remove/wrap affordances.
    return (
      <div className="space-y-2">
        <KindPicker value={value.kind} onChange={(k) => onChange(newCondition(k))} />
        {editor}
      </div>
    );
  }

  // Root: tabs + live preview.
  return <RootShell value={value} onChange={onChange} editor={editor} />;
}

function RootShell({
  value,
  onChange,
  editor,
}: {
  value: Condition;
  onChange: (next: Condition) => void;
  editor: React.ReactNode;
}) {
  const [tab, setTab] = useState<"builder" | "raw">("builder");
  const [rawBuffer, setRawBuffer] = useState("");
  let previewText = "";
  try {
    previewText = formatCondition(value);
  } catch {
    previewText = "(preview unavailable — condition mid-edit)";
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <TabButton active={tab === "builder"} onClick={() => setTab("builder")}>
          Builder
        </TabButton>
        <TabButton active={tab === "raw"} onClick={() => setTab("raw")}>
          Raw JSON
        </TabButton>
        <div className="ml-auto rounded-md bg-muted px-2 py-1 font-mono text-[11px] text-muted-foreground">
          <span className="mr-1 uppercase tracking-wide text-[10px]">preview:</span>
          {previewText}
        </div>
      </div>

      {tab === "builder" ? (
        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <Label className="text-xs">Kind</Label>
            <KindPicker
              value={value.kind}
              onChange={(k) => onChange(newCondition(k))}
            />
          </div>
          {editor}
        </div>
      ) : (
        <RawJsonPane
          value={value}
          onApply={(next) => {
            onChange(next);
            setTab("builder");
          }}
          buffer={rawBuffer}
          onBufferChange={setRawBuffer}
        />
      )}
    </div>
  );
}

function KindPicker({
  value,
  onChange,
}: {
  value: Condition["kind"];
  onChange: (kind: PrimitiveKind | ComposerKind) => void;
}) {
  return (
    <select
      className="h-8 rounded-md border border-input bg-background px-2 font-mono text-[11px]"
      value={value}
      onChange={(e) => onChange(e.target.value as PrimitiveKind | ComposerKind)}
    >
      <optgroup label="Primitives">
        {PRIMITIVE_KINDS.map((k) => (
          <option key={k} value={k}>
            {KIND_LABELS[k]}
          </option>
        ))}
      </optgroup>
      <optgroup label="Composers">
        {COMPOSER_KINDS.map((k) => (
          <option key={k} value={k}>
            {KIND_LABELS[k]}
          </option>
        ))}
      </optgroup>
    </select>
  );
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Button
      type="button"
      size="sm"
      variant={active ? "default" : "outline"}
      className="h-7 text-[11px]"
      onClick={onClick}
    >
      {children}
    </Button>
  );
}

// Re-export for tests / consumers that want the util set.
export { COMPOSER_SET };
