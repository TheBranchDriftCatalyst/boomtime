import { useNavigate } from "react-router";
import { Boxes, Download, Layers, Wand2 } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import { useFirstRunWelcome } from "@/features/onboarding/useFirstRunWelcome";

// First-run welcome modal: shown on the user's first visit to /app in this
// browser. localStorage flag `boomtime-welcomed` gates redisplay (a fresh
// incognito window re-shows it). Mounted at AppShell so it floats above any
// dashboard route without blocking ProtectedRoute logic.
export function WelcomeModal() {
  const { open, dismiss } = useFirstRunWelcome();
  const navigate = useNavigate();

  function goImport() {
    dismiss();
    navigate("/app/import");
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && dismiss()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Welcome to Boomtime</DialogTitle>
          <DialogDescription>
            A self-hosted, Wakatime-compatible coding-time tracker. Point your
            editor plugin at this server and your keystrokes turn into
            dashboards. Here's the quick tour.
          </DialogDescription>
        </DialogHeader>

        <ul className="space-y-3 text-sm">
          <li className="flex gap-3">
            <Download className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
            <div>
              <div className="font-medium">Import history</div>
              <div className="text-muted-foreground">
                Pull your existing Wakatime data by date range — a first-class
                migration path, not an afterthought.
              </div>
            </div>
          </li>
          <li className="flex gap-3">
            <Wand2 className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
            <div>
              <div className="font-medium">Curation</div>
              <div className="text-muted-foreground">
                Rename or hide projects, languages, and machines to keep the
                view honest.
              </div>
            </div>
          </li>
          <li className="flex gap-3">
            <Boxes className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
            <div>
              <div className="font-medium">Spaces</div>
              <div className="text-muted-foreground">
                Named, rule-based scopes — group work by client, product, or
                whatever axis matters.
              </div>
            </div>
          </li>
          <li className="flex gap-3">
            <Layers className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
            <div>
              <div className="font-medium">Widgets</div>
              <div className="text-muted-foreground">
                Embed compact stat cards in READMEs and dashboards. Roll links
                to invalidate; scope them per space.
              </div>
            </div>
          </li>
        </ul>

        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="secondary" onClick={dismiss}>
            Skip for now
          </Button>
          <Button onClick={goImport}>Import Wakatime data</Button>
        </DialogFooter>

        <p className="mt-2 text-center text-[11px] text-muted-foreground/70">
          Made by{" "}
          <a
            href="https://github.com/TheBranchDriftCatalyst"
            target="_blank"
            rel="noreferrer"
            className="underline-offset-2 hover:text-foreground hover:underline"
          >
            Catalyst Development
          </a>
        </p>
      </DialogContent>
    </Dialog>
  );
}
