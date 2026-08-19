import { Copy } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { copyToClipboard } from "@/lib/utils";
import { cn } from "@/lib/utils";

interface JsonBlockProps {
  value: unknown;
  className?: string;
}

/** Pretty-printed JSON with a copy button, dark-mode friendly. */
export function JsonBlock({ value, className }: JsonBlockProps) {
  const text = JSON.stringify(value, null, 2);
  return (
    <div className={cn("relative", className)}>
      <Button
        variant="secondary"
        size="sm"
        className="absolute right-2 top-2 z-10"
        onClick={async () => {
          await copyToClipboard(text);
          toast.success("Copied JSON to clipboard");
        }}
      >
        <Copy className="h-3.5 w-3.5" />
        Copy
      </Button>
      <pre className="max-h-[28rem] overflow-auto rounded-md border bg-muted/40 p-3 pr-20 font-mono text-xs leading-relaxed">
        {text}
      </pre>
    </div>
  );
}
