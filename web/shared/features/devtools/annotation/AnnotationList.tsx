import { useAnnotationContext, type Annotation } from "@shared/features/devtools/context";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import { Trash2, FileText, Bug, CheckSquare, BookOpen } from "lucide-react";
import { cn } from "@shared/lib/utils";

interface AnnotationListProps {
  /**
   * Optional filter by component name
   */
  componentName?: string;
  /**
   * Optional filter by type
   */
  type?: Annotation["type"];
  /**
   * Optional filter by priority
   */
  priority?: Annotation["priority"];
}

const typeIcons = {
  todo: CheckSquare,
  bug: Bug,
  note: FileText,
  docs: BookOpen,
};

const typeColors = {
  todo: "bg-blue-500/10 text-blue-500 border-blue-500/20",
  bug: "bg-red-500/10 text-red-500 border-red-500/20",
  note: "bg-yellow-500/10 text-yellow-500 border-yellow-500/20",
  docs: "bg-purple-500/10 text-purple-500 border-purple-500/20",
};

const priorityColors = {
  low: "bg-gray-500/10 text-gray-500 border-gray-500/20",
  medium: "bg-orange-500/10 text-orange-500 border-orange-500/20",
  high: "bg-red-500/10 text-red-500 border-red-500/20",
};

/**
 * List view of annotations with filtering and delete functionality
 */
export function AnnotationList({ componentName, type, priority }: AnnotationListProps) {
  const { getAllAnnotations, removeAnnotation } = useAnnotationContext();

  const annotations = getAllAnnotations().filter(annotation => {
    if (componentName && annotation.componentName !== componentName) return false;
    if (type && annotation.type !== type) return false;
    if (priority && annotation.priority !== priority) return false;
    return true;
  });

  // Sort by timestamp (newest first)
  const sortedAnnotations = [...annotations].sort((a, b) => b.timestamp - a.timestamp);

  if (sortedAnnotations.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-center">
        <FileText className="h-12 w-12 text-muted-foreground/50 mb-4" />
        <p className="text-sm text-muted-foreground">
          No annotations found
          {componentName && ` for ${componentName}`}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {sortedAnnotations.map(annotation => {
        const TypeIcon = typeIcons[annotation.type];
        const formattedDate = new Date(annotation.timestamp).toLocaleString();

        return (
          <div
            key={annotation.id}
            className="group relative rounded-lg border bg-card p-4 transition hover:shadow-md"
          >
            <div className="flex items-start justify-between gap-3">
              <div className="flex items-start gap-3 flex-1 min-w-0">
                <div className={cn("rounded-md p-2", typeColors[annotation.type])}>
                  <TypeIcon className="h-4 w-4" />
                </div>

                <div className="flex-1 min-w-0 space-y-2">
                  <div className="flex items-center gap-2 flex-wrap">
                    <code className="text-sm font-mono bg-muted px-2 py-0.5 rounded">
                      {annotation.componentName}
                    </code>
                    <Badge variant="outline" className={cn("text-xs", typeColors[annotation.type])}>
                      {annotation.type}
                    </Badge>
                    <Badge
                      variant="outline"
                      className={cn("text-xs", priorityColors[annotation.priority])}
                    >
                      {annotation.priority}
                    </Badge>
                  </div>

                  <p className="text-sm text-foreground whitespace-pre-wrap break-words">
                    {annotation.note}
                  </p>

                  <p className="text-xs text-muted-foreground">{formattedDate}</p>
                </div>
              </div>

              <Button
                variant="ghost"
                size="icon"
                className="opacity-0 group-hover:opacity-100 transition-opacity shrink-0"
                onClick={() => removeAnnotation(annotation.id)}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          </div>
        );
      })}
    </div>
  );
}
