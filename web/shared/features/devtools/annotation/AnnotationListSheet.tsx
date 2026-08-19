import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { Download, Trash2, FileJson, FileText, CheckSquare } from "lucide-react";
import { AnnotationList } from "./AnnotationList";
import { useAnnotationContext } from "@shared/features/devtools/context";
import { exportAsJSON, exportAsMarkdown, exportAsTODO } from "./utils/exporters";

interface AnnotationListSheetProps {
  /**
   * Whether the sheet is open
   */
  open: boolean;
  /**
   * Callback when sheet should close
   */
  onOpenChange: (open: boolean) => void;
}

/**
 * Right side sheet displaying list of all annotations
 *
 * Features:
 * - View all annotations in a list
 * - Export and clear actions
 *
 * boomtime note: backend sync is disabled (localStorage-only), so the sync
 * status indicator / manual-sync button have been dropped from this fork.
 */
export function AnnotationListSheet({ open, onOpenChange }: AnnotationListSheetProps) {
  const { getAllAnnotations, clearAll } = useAnnotationContext();

  const annotations = getAllAnnotations();

  const handleClearAll = () => {
    if (confirm("Are you sure you want to delete all annotations? This cannot be undone.")) {
      clearAll();
    }
  };

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-[400px] sm:w-[540px] overflow-y-auto">
        <SheetHeader>
          <SheetTitle>Annotations</SheetTitle>
          <SheetDescription>View and manage all your component annotations</SheetDescription>
        </SheetHeader>

        <div className="mt-6 space-y-4">
          {/* Controls */}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              {annotations.length > 0 && (
                <span className="text-xs text-muted-foreground">
                  {annotations.length} annotation{annotations.length !== 1 ? "s" : ""}
                </span>
              )}
            </div>

            <div className="flex items-center gap-2">
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon"
                    disabled={annotations.length === 0}
                    title="Export annotations"
                  >
                    <Download className="h-4 w-4" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onClick={() => exportAsJSON(annotations)}>
                    <FileJson className="mr-2 h-4 w-4" />
                    Export as JSON
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => exportAsMarkdown(annotations)}>
                    <FileText className="mr-2 h-4 w-4" />
                    Export as Markdown
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => exportAsTODO(annotations)}>
                    <CheckSquare className="mr-2 h-4 w-4" />
                    Export as TODO.md
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>

              <Button
                variant="ghost"
                size="icon"
                onClick={handleClearAll}
                disabled={annotations.length === 0}
                title="Clear all annotations"
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          </div>

          {/* Annotation List */}
          <div className="mt-4">
            <AnnotationList />
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}
