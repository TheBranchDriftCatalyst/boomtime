// gaka-esv: dropdown that adds a curation rule's matchValue to a Space's
// membership. Reuses the existing POST /spaces/:id/rules endpoint — this
// feature is pure FE composition; nothing new on the backend.
//
// Interpretation (B) was chosen (see the bead's --design field): curation
// rules are global rewrites; Spaces are inclusion-scopes. "Add this rule's
// value to a Space" means "create a new space_rules row targeting this
// axis+matchValue" — the curation rule is untouched, and the new membership
// rule uses the same matchType semantics.
//
// matchType translation: curation supports exact/regex/template; space_rules
// only accept exact/regex. A template curation rule's matchValue is still a
// regex pattern (the template only affects the target), so we degrade
// template → regex when quick-adding to a Space.

import { useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import { Boxes, Loader2, Plus } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { useSpaceMutations, useSpaces } from "@boomtime/features/spaces/useSpaces";
import type { CurationRule, SpaceMatchType } from "@shared/types/api";

// Map curation matchType → space matchType. template is a rename-only
// transform; when quick-adding to a Space it degrades to `regex` (the
// pattern still matches the same rows; the transform is dropped).
function toSpaceMatchType(rule: CurationRule): SpaceMatchType {
  const mt = rule.matchType ?? "exact";
  if (mt === "regex" || mt === "template") return "regex";
  return "exact";
}

export function AddToSpaceDropdown({ rule }: { rule: CurationRule }) {
  const navigate = useNavigate();
  const spaces = useSpaces();
  const { addRule, create: createSpace } = useSpaceMutations();
  const [open, setOpen] = useState(false);
  // "New space with THIS value" inline flow — user types the name, hits
  // Enter, we create the Space and immediately add the rule. Keeps the
  // interaction in ONE popover; no dialog handoff.
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [filter, setFilter] = useState("");

  const filtered = useMemo(() => {
    const all = spaces.data ?? [];
    const q = filter.trim().toLowerCase();
    if (!q) return all;
    return all.filter((s) => s.name.toLowerCase().includes(q));
  }, [spaces.data, filter]);

  const spaceMatchType = toSpaceMatchType(rule);

  function addToExisting(spaceId: number, spaceName: string) {
    addRule.mutate(
      {
        id: spaceId,
        body: {
          axis: rule.axis,
          matchValue: rule.matchValue,
          matchType: spaceMatchType,
        },
      },
      {
        onSuccess: () => {
          toast.success(`Added ${rule.matchValue} to ${spaceName}`);
          setOpen(false);
        },
        // Backend returns a fixed "Could not add space rule (invalid
        // pattern?)" for anything that fails validation (regex compile,
        // owner mismatch, etc.); surface it verbatim — the user already
        // saw a valid rule on-screen, so this is genuinely surprising.
        onError: () => toast.error("Failed to add to space"),
      },
    );
  }

  function submitCreate() {
    const name = newName.trim();
    if (!name) return;
    createSpace.mutate(name, {
      onSuccess: (space) => {
        // Chain: create → add rule → toast → close.
        addRule.mutate(
          {
            id: space.id,
            body: {
              axis: rule.axis,
              matchValue: rule.matchValue,
              matchType: spaceMatchType,
            },
          },
          {
            onSuccess: () => {
              toast.success(`Created ${name} with ${rule.matchValue}`);
              setOpen(false);
              setCreating(false);
              setNewName("");
            },
            // Rare: Space created but the rule add failed. Tell the user
            // + drop them onto the new Space so they can retry the rule
            // inline (the Space is not broken, it's just empty).
            onError: () => {
              toast.error(
                `Created ${name} but couldn't add the rule — open the Space to try again`,
              );
              setOpen(false);
              setCreating(false);
              setNewName("");
              navigate(`/app/space/${space.id}`);
            },
          },
        );
      },
      onError: () => toast.error("Failed to create space"),
    });
  }

  const busy = addRule.isPending || createSpace.isPending;
  const spacesEmpty = !spaces.isLoading && (spaces.data?.length ?? 0) === 0;

  return (
    <DropdownMenu
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) {
          // Reset the inline-create state when the popover closes so
          // reopening starts from a clean list view.
          setCreating(false);
          setNewName("");
          setFilter("");
        }
      }}
    >
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          title={`Add "${rule.matchValue}" to a space`}
          aria-label={`Add ${rule.matchValue} to a space`}
          className="rounded-full p-0.5 text-muted-foreground hover:bg-background hover:text-foreground"
        >
          <Boxes className="h-3.5 w-3.5" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel>Add to space</DropdownMenuLabel>
        <div className="px-2 pb-1 text-xs text-muted-foreground">
          {rule.axis} = <span className="font-mono">{rule.matchValue}</span>
        </div>
        <DropdownMenuSeparator />

        {spaces.isLoading ? (
          <div className="flex items-center gap-2 px-2 py-1.5 text-xs text-muted-foreground">
            <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading spaces…
          </div>
        ) : spacesEmpty && !creating ? (
          // Empty state: nudge the user to bootstrap. Two affordances so
          // they can create the Space right here OR jump to the Spaces
          // area of the app to build one with more care.
          <>
            <div className="px-2 py-1.5 text-xs text-muted-foreground">
              You don't have any spaces yet.
            </div>
            <DropdownMenuItem
              onSelect={(e) => {
                e.preventDefault();
                setCreating(true);
              }}
            >
              <Plus className="mr-2 h-3.5 w-3.5" /> Create your first space
            </DropdownMenuItem>
          </>
        ) : (
          <>
            {/*
              Filter input — small, no icon, autoFocus so a keyboard user
              can start typing immediately. Not the empty-state path so we
              only render it when there ARE spaces to filter.
            */}
            {!creating && (spaces.data?.length ?? 0) > 0 && (
              <div className="px-2 py-1">
                <Input
                  autoFocus
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  onKeyDown={(e) => {
                    // Radix DropdownMenu intercepts arrow keys for menu
                    // navigation, which pulls focus off the input on
                    // ArrowDown. That's actually what we want — let the
                    // user narrow with typing, then arrow-down to select.
                    // Just stop Enter from closing the menu prematurely.
                    if (e.key === "Enter") e.preventDefault();
                  }}
                  placeholder="Filter spaces…"
                  className="h-7 text-xs"
                />
              </div>
            )}
            {!creating && filtered.length === 0 && (spaces.data?.length ?? 0) > 0 && (
              <div className="px-2 py-1.5 text-xs text-muted-foreground">
                No spaces match "{filter}".
              </div>
            )}
            {!creating &&
              filtered.map((s) => (
                <DropdownMenuItem
                  key={s.id}
                  disabled={busy}
                  onSelect={(e) => {
                    e.preventDefault();
                    addToExisting(s.id, s.name);
                  }}
                >
                  {s.name}
                  <span className="ml-auto text-xs text-muted-foreground">
                    {s.ruleCount} {s.ruleCount === 1 ? "rule" : "rules"}
                  </span>
                </DropdownMenuItem>
              ))}
          </>
        )}

        {!creating && !spaces.isLoading && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={(e) => {
                e.preventDefault();
                setCreating(true);
              }}
            >
              <Plus className="mr-2 h-3.5 w-3.5" /> New space with this value…
            </DropdownMenuItem>
          </>
        )}

        {creating && (
          <div className="space-y-2 px-2 py-2">
            <div className="text-xs text-muted-foreground">
              Create a new space and add this rule.
            </div>
            <Input
              autoFocus
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  submitCreate();
                }
                if (e.key === "Escape") {
                  e.preventDefault();
                  setCreating(false);
                  setNewName("");
                }
              }}
              placeholder="Work"
              className="h-7 text-xs"
              disabled={busy}
            />
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => {
                  setCreating(false);
                  setNewName("");
                }}
                disabled={busy}
                className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-background hover:text-foreground disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={submitCreate}
                disabled={busy || newName.trim() === ""}
                className="rounded-md bg-primary px-2 py-1 text-xs text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {busy ? "Creating…" : "Create + Add"}
              </button>
            </div>
          </div>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
