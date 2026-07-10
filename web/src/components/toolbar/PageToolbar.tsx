import type { ReactNode } from "react";

interface PageToolbarProps {
  title: string;
  children?: ReactNode;
}

export function PageToolbar({ title, children }: PageToolbarProps) {
  return (
    <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
      <div className="flex flex-wrap items-center gap-2">{children}</div>
    </div>
  );
}
