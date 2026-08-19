import { useState } from "react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { FileText, List, MousePointer2 } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import { AnnotationListSheet } from "./AnnotationListSheet";
import { AnnotationFormSheet } from "./AnnotationFormSheet";
import { ComponentInspector, type ComponentInfo } from "./ComponentInspector";
import { useAnnotationContext } from "@shared/features/devtools/context";

interface AnnotationToggleProps {
  /**
   * Button variant
   */
  variant?: "default" | "ghost" | "outline" | "secondary" | "link";
  /**
   * Button size
   */
  size?: "default" | "sm" | "lg" | "icon";
  /**
   * Show annotation count badge
   */
  showCount?: boolean;
}

/**
 * Toggle button with dropdown menu for annotation features
 *
 * Provides two options:
 * 1. View Annotations - Opens right side sheet with annotation list
 * 2. Inspect Component - Activates inspector mode to select a component
 */
export function AnnotationToggle({
  variant = "ghost",
  size = "default",
  showCount = true,
}: AnnotationToggleProps) {
  const [listOpen, setListOpen] = useState(false);
  const [formOpen, setFormOpen] = useState(false);
  const [inspectorActive, setInspectorActive] = useState(false);
  const [selectedComponent, setSelectedComponent] = useState<ComponentInfo | null>(null);

  const { getAllAnnotations } = useAnnotationContext();
  const annotationCount = getAllAnnotations().length;

  const handleInspectClick = () => {
    setInspectorActive(true);
  };

  const handleComponentSelect = (info: ComponentInfo) => {
    setSelectedComponent(info);
    setInspectorActive(false);
    setFormOpen(true);
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant={variant} size={size} className="relative gap-2">
            <FileText className="h-4 w-4" />
            {size !== "icon" && "Annotations"}
            {showCount && annotationCount > 0 && (
              <Badge
                variant="secondary"
                className="ml-1 h-5 min-w-[20px] rounded-full px-1 text-xs"
              >
                {annotationCount}
              </Badge>
            )}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => setListOpen(true)}>
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
      <AnnotationListSheet open={listOpen} onOpenChange={setListOpen} />

      {/* Annotation Form (Bottom Sheet) */}
      <AnnotationFormSheet
        open={formOpen}
        onOpenChange={setFormOpen}
        selectedComponent={selectedComponent}
      />
    </>
  );
}
