import type { Annotation } from "@shared/features/devtools/context";

/**
 * Export annotations as JSON
 */
export function exportAsJSON(annotations: Annotation[], filename?: string): void {
  const dataStr = JSON.stringify(annotations, null, 2);
  const blob = new Blob([dataStr], { type: "application/json" });
  downloadBlob(blob, filename || `annotations-${Date.now()}.json`);
}

/**
 * Export annotations as Markdown
 */
export function exportAsMarkdown(annotations: Annotation[], filename?: string): void {
  const markdown = generateMarkdown(annotations);
  const blob = new Blob([markdown], { type: "text/markdown" });
  downloadBlob(blob, filename || `annotations-${Date.now()}.md`);
}

/**
 * Export annotations as TODO.md format
 * Groups by type and priority
 */
export function exportAsTODO(annotations: Annotation[], filename?: string): void {
  const todo = generateTODOFormat(annotations);
  const blob = new Blob([todo], { type: "text/markdown" });
  downloadBlob(blob, filename || `TODO-${Date.now()}.md`);
}

/**
 * Generate Markdown content from annotations
 */
function generateMarkdown(annotations: Annotation[]): string {
  if (annotations.length === 0) {
    return "# Annotations\n\nNo annotations found.\n";
  }

  // Group by component
  const byComponent = annotations.reduce(
    (acc, annotation) => {
      if (!acc[annotation.componentName]) {
        acc[annotation.componentName] = [];
      }
      acc[annotation.componentName].push(annotation);
      return acc;
    },
    {} as Record<string, Annotation[]>
  );

  let markdown = "# Component Annotations\n\n";
  markdown += `Generated: ${new Date().toLocaleString()}\n\n`;
  markdown += `Total Annotations: ${annotations.length}\n\n`;
  markdown += "---\n\n";

  // Sort components alphabetically
  const sortedComponents = Object.keys(byComponent).sort();

  sortedComponents.forEach(componentName => {
    markdown += `## ${componentName}\n\n`;

    // Sort annotations by priority (high -> medium -> low) then timestamp
    const sortedAnnotations = byComponent[componentName].sort((a, b) => {
      const priorityOrder = { high: 0, medium: 1, low: 2 };
      const priorityDiff = priorityOrder[a.priority] - priorityOrder[b.priority];
      if (priorityDiff !== 0) return priorityDiff;
      return b.timestamp - a.timestamp;
    });

    sortedAnnotations.forEach(annotation => {
      const emoji = getTypeEmoji(annotation.type);
      const priorityBadge = getPriorityBadge(annotation.priority);
      const date = new Date(annotation.timestamp).toLocaleString();

      markdown += `### ${emoji} ${annotation.type.toUpperCase()} ${priorityBadge}\n\n`;
      markdown += `${annotation.note}\n\n`;
      markdown += `*Created: ${date}*\n\n`;
    });

    markdown += "---\n\n";
  });

  return markdown;
}

/**
 * Generate TODO.md format from annotations
 */
function generateTODOFormat(annotations: Annotation[]): string {
  if (annotations.length === 0) {
    return "# TODO\n\nNo tasks found.\n";
  }

  let todo = "# TODO\n\n";
  todo += `Generated: ${new Date().toLocaleString()}\n\n`;

  // Group by type
  const byType = annotations.reduce(
    (acc, annotation) => {
      if (!acc[annotation.type]) {
        acc[annotation.type] = [];
      }
      acc[annotation.type].push(annotation);
      return acc;
    },
    {} as Record<string, Annotation[]>
  );

  // Process each type
  const typeOrder: Array<Annotation["type"]> = ["bug", "todo", "docs", "note"];

  typeOrder.forEach(type => {
    if (!byType[type] || byType[type].length === 0) return;

    const emoji = getTypeEmoji(type);
    todo += `## ${emoji} ${type.toUpperCase()}\n\n`;

    // Group by priority within type
    const byPriority = byType[type].reduce(
      (acc, annotation) => {
        if (!acc[annotation.priority]) {
          acc[annotation.priority] = [];
        }
        acc[annotation.priority].push(annotation);
        return acc;
      },
      {} as Record<string, Annotation[]>
    );

    // Process priorities: high -> medium -> low
    const priorityOrder: Array<Annotation["priority"]> = ["high", "medium", "low"];

    priorityOrder.forEach(priority => {
      if (!byPriority[priority] || byPriority[priority].length === 0) return;

      todo += `### ${getPriorityBadge(priority)}\n\n`;

      byPriority[priority].forEach(annotation => {
        todo += `- [ ] **${annotation.componentName}**: ${annotation.note}\n`;
      });

      todo += "\n";
    });
  });

  return todo;
}

/**
 * Get emoji for annotation type
 */
function getTypeEmoji(type: Annotation["type"]): string {
  const emojis = {
    todo: "📝",
    bug: "🐛",
    note: "💡",
    docs: "📚",
  };
  return emojis[type];
}

/**
 * Get badge text for priority
 */
function getPriorityBadge(priority: Annotation["priority"]): string {
  const badges = {
    high: "🔴 High Priority",
    medium: "🟡 Medium Priority",
    low: "🟢 Low Priority",
  };
  return badges[priority];
}

/**
 * Helper to download a blob as a file
 */
function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}
