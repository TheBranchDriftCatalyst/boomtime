import { Clock } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { TIME_LIMIT_OPTIONS } from "@/lib/config";

interface TimeLimitDropdownProps {
  value: number;
  onChange: (n: number) => void;
}

export function TimeLimitDropdown({ value, onChange }: TimeLimitDropdownProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          title="Maximum time allowed between heartbeats when calculating your total coding activity."
        >
          <Clock className="h-4 w-4" />
          Timeout ({value} mins)
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {TIME_LIMIT_OPTIONS.map((n) => (
          <DropdownMenuItem key={n} onSelect={() => onChange(n)}>
            {n} mins
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
