import { useState } from "react";
import { Menu } from "lucide-react";
import {
  Sheet,
  SheetContent,
  SheetTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import { SidebarBody } from "@/layout/Sidebar";

interface MobileNavProps {
  onLogout: () => void;
  onCreateSpace: () => void;
}

/** MobileNav — the phone/tablet navigation (gaka-k26n.1). Below md the desktop
 * rail is hidden, so this hamburger lives in the header and opens the full nav
 * as a left-side Sheet drawer. It reuses SidebarBody verbatim (always expanded,
 * no collapse toggle) so the drawer and the rail never drift, and every nav
 * action closes the drawer via onNavigate. */
export function MobileNav({ onLogout, onCreateSpace }: MobileNavProps) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <button
        type="button"
        aria-label="Open navigation menu"
        onClick={() => setOpen(true)}
        className="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:hidden"
      >
        <Menu className="h-5 w-5" />
      </button>

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent
          side="left"
          className="flex w-72 max-w-[85vw] flex-col gap-0 border-r bg-sidebar p-0 text-sidebar-foreground"
        >
          {/* Radix Dialog requires an accessible title; the brand row is
              decorative so we provide a screen-reader-only one. */}
          <SheetTitle className="sr-only">Navigation</SheetTitle>
          <SidebarBody
            collapsed={false}
            showCollapseToggle={false}
            onLogout={() => {
              setOpen(false);
              onLogout();
            }}
            onCreateSpace={() => {
              setOpen(false);
              onCreateSpace();
            }}
            onNavigate={() => setOpen(false)}
          />
        </SheetContent>
      </Sheet>
    </>
  );
}
