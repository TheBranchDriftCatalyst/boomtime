// Shared Suspense placeholder for lazy route chunks.
//
// Lives in the shared boundary (not a domain) so every domain's registered
// route can wrap its lazy page body in the SAME fallback the shell has always
// used — a centered Spinner sized like the router's own bootstrap Spinner, so a
// route switch and the initial boot look identical.
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";

export function PageFallback() {
  return (
    <div className="flex h-[60vh] items-center justify-center">
      <Spinner />
    </div>
  );
}
