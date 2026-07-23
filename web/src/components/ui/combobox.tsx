import { useMemo, useRef, useState } from "react";
import { Check, ChevronsUpDown, Plus } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/popover";
import {
  canCreate as canCreateFn,
  filterOptions,
} from "@/lib/comboboxFilter";
import { cn } from "@/lib/utils";

export interface ComboboxOption {
  value: string;
  // Optional right-aligned meta (e.g. a heartbeat count).
  count?: number;
}

interface ComboboxProps {
  options: ComboboxOption[];
  value: string | null;
  onSelect: (value: string) => void;
  placeholder?: string;
  searchPlaceholder?: string;
  emptyText?: string;
  /** Allow choosing a typed value that isn't in the list (merge/new name). */
  creatable?: boolean;
  disabled?: boolean;
  loading?: boolean;
  className?: string;
  /** Render the trigger full-width (forms) vs auto (inline). */
  fullWidth?: boolean;
}

/**
 * Searchable combobox built on Radix Popover — no extra deps. Filters options
 * client-side, shows an optional count per option, and (when `creatable`) lets
 * the user commit a brand-new typed value. Dark-mode native via theme tokens.
 */
export function Combobox({
  options,
  value,
  onSelect,
  placeholder = "Select...",
  searchPlaceholder = "Search...",
  emptyText = "No results.",
  creatable = false,
  disabled = false,
  loading = false,
  className,
  fullWidth = true,
}: ComboboxProps) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  const filtered = useMemo(
    () => filterOptions(options, search),
    [options, search],
  );
  const canCreate = useMemo(
    () => canCreateFn(options, search, creatable),
    [options, search, creatable],
  );

  function commit(v: string) {
    onSelect(v);
    setSearch("");
    setOpen(false);
  }

  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) setSearch("");
      }}
    >
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          role="combobox"
          aria-expanded={open}
          disabled={disabled}
          className={cn(
            "justify-between font-normal",
            fullWidth && "w-full",
            !value && "text-muted-foreground",
            className,
          )}
        >
          <span className="truncate">{value || placeholder}</span>
          <ChevronsUpDown className="h-4 w-4 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-[--radix-popover-trigger-width] min-w-56 p-0"
        // onOpenAutoFocus is a native DOM event handler from Radix, but
        // catalyst-ui's PopoverContentProps also extends HTMLAttributes which
        // types onOpenAutoFocus as a React synthetic focus handler. Cast
        // around the collision — behavior is the native DOM event at runtime.
        {...({
          onOpenAutoFocus: (e: Event) => {
            e.preventDefault();
            inputRef.current?.focus();
          },
        } as unknown as React.ComponentProps<typeof PopoverContent>)}
      >
        <div className="border-b p-2">
          <Input
            ref={inputRef}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                if (filtered.length > 0) commit(filtered[0].value);
                else if (canCreate) commit(search.trim());
              }
            }}
            placeholder={searchPlaceholder}
            className="h-8"
          />
        </div>
        <div className="max-h-60 overflow-y-auto p-1">
          {loading ? (
            <p className="px-2 py-4 text-center text-sm text-muted-foreground">
              Loading...
            </p>
          ) : (
            <>
              {canCreate && (
                <button
                  type="button"
                  onClick={() => commit(search.trim())}
                  className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                >
                  <Plus className="h-4 w-4" />
                  Use &quot;{search.trim()}&quot;
                </button>
              )}
              {filtered.length === 0 && !canCreate ? (
                <p className="px-2 py-4 text-center text-sm text-muted-foreground">
                  {emptyText}
                </p>
              ) : (
                filtered.map((opt) => (
                  <button
                    key={opt.value}
                    type="button"
                    onClick={() => commit(opt.value)}
                    className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                  >
                    <Check
                      className={cn(
                        "h-4 w-4 shrink-0",
                        value === opt.value ? "opacity-100" : "opacity-0",
                      )}
                    />
                    <span className="truncate">{opt.value}</span>
                    {opt.count !== undefined && (
                      <span className="ml-auto shrink-0 font-mono text-xs text-muted-foreground">
                        {opt.count.toLocaleString()}
                      </span>
                    )}
                  </button>
                ))
              )}
            </>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
