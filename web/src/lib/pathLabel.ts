// Shorten a file path to "<parent>/<filename>" for compact display (the full
// path goes in a title tooltip). Shared by the FileBar chart and the
// cross-project files table so the two never drift.
export function shortPath(entity: string): string {
  const parts = entity.split("/").filter(Boolean);
  const filename = parts[parts.length - 1] ?? entity;
  const parent = parts[parts.length - 2];
  return parent ? `${parent}/${filename}` : filename;
}
