import { useState } from "react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { Code2, List, MousePointer2 } from "lucide-react";
import type { ComponentInfo } from "../annotation/ComponentInspector";
import { ComponentInspector } from "../annotation/ComponentInspector";
import { AnnotationListSheet } from "../annotation/AnnotationListSheet";
import { AnnotationFormSheet } from "../annotation/AnnotationFormSheet";

interface DevModeToggleProps {
  /**
   * Button variant
   */
  variant?: "default" | "outline" | "ghost" | "secondary";
  /**
   * Button size
   */
  size?: "default" | "sm" | "lg" | "icon";
}

/**
 * DevModeToggle - Main entry point for boomtime's admin-only devtools.
 *
 * Features:
 * - Annotations (view list, inspect components)
 *
 * boomtime note: this is the upstream catalyst-ui DevModeToggle with the
 * entire i18n / "View Translations" section removed. Visibility is gated by
 * the caller — HeaderBar renders it only when `useIsAdmin()` is true — so
 * there is no internal build-env gate here; it always renders when mounted.
 */
export function DevModeToggle({ variant = "outline", size = "icon" }: DevModeToggleProps) {
  // Annotation state
  const [annotationListOpen, setAnnotationListOpen] = useState(false);
  const [annotationFormOpen, setAnnotationFormOpen] = useState(false);
  const [inspectorActive, setInspectorActive] = useState(false);
  const [selectedComponent, setSelectedComponent] = useState<ComponentInfo | null>(null);

  const handleInspectClick = () => {
    setInspectorActive(true);
  };

  const handleComponentSelect = (info: ComponentInfo) => {
    setSelectedComponent(info);
    setInspectorActive(false);
    setAnnotationFormOpen(true);
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant={variant} size={size} title="Dev Mode Utilities">
            <Code2 className="h-4 w-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuLabel>Dev Mode</DropdownMenuLabel>
          <DropdownMenuSeparator />

          {/* Annotations Section */}
          <DropdownMenuLabel className="text-xs text-muted-foreground font-normal px-2">
            Annotations
          </DropdownMenuLabel>
          <DropdownMenuItem onClick={() => setAnnotationListOpen(true)}>
            <List className="mr-2 h-4 w-4" />
            View Annotations
          </DropdownMenuItem>
          <DropdownMenuItem onClick={handleInspectClick}>
            <MousePointer2 className="mr-2 h-4 w-4" />
            Inspect Component
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {/* Global Component Inspector */}
      <ComponentInspector
        active={inspectorActive}
        onToggle={setInspectorActive}
        onComponentSelect={handleComponentSelect}
      />

      {/* Annotation List (Right Side Sheet) */}
      <AnnotationListSheet open={annotationListOpen} onOpenChange={setAnnotationListOpen} />

      {/* Annotation Form (Bottom Sheet) */}
      <AnnotationFormSheet
        open={annotationFormOpen}
        onOpenChange={setAnnotationFormOpen}
        selectedComponent={selectedComponent}
      />
    </>
  );
}
