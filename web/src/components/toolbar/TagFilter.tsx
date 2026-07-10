import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Tag, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { api } from "@/lib/api";

interface TagFilterProps {
  value: string | null;
  onChange: (tag: string | null) => void;
}

/** Tag autocomplete filter (replaces the old @tarekraafat/autocomplete.js). */
export function TagFilter({ value, onChange }: TagFilterProps) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");

  const { data } = useQuery({
    queryKey: ["tags"],
    queryFn: () => api.getUserTags(),
    staleTime: 60_000,
  });

  const filtered = useMemo(() => {
    const tags = data?.tags ?? [];
    return tags.filter((t) =>
      t.toLowerCase().includes(search.toLowerCase()),
    );
  }, [data, search]);

  if (value) {
    return (
      <Button
        variant="secondary"
        size="sm"
        onClick={() => onChange(null)}
        title="Clear tag filter"
      >
        <Tag className="h-4 w-4" />#{value}
        <X className="h-3.5 w-3.5" />
      </Button>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm">
          <Tag className="h-4 w-4" />
          Filter by tag
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-56 p-2">
        <Input
          autoFocus
          placeholder="Search tags..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="mb-2"
        />
        <div className="max-h-56 overflow-y-auto">
          {filtered.length === 0 ? (
            <p className="px-2 py-3 text-center text-sm text-muted-foreground">
              No tags found
            </p>
          ) : (
            filtered.slice(0, 20).map((t) => (
              <button
                key={t}
                className="w-full rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                onClick={() => {
                  onChange(t);
                  setOpen(false);
                  setSearch("");
                }}
              >
                #{t}
              </button>
            ))
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
