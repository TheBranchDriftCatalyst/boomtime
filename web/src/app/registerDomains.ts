// Host composition root — the ONE place the boomtime host app decides which
// domains register their FE surface (nav / settings / admin). This is the entry
// seam the coordinator's dependency-inversion hinges on:
//
//   - the HOST app (this file) registers BOTH domains + core, so the full app
//     has every nav entry, settings tab, and admin tab.
//   - a future STANDALONE books app would ship its own composition that calls
//     ONLY registerCoreDomain() + registerBooksDomain(); registerBoomtimeDomain
//     is never imported there, so the bundler drops every code-domain module.
//
// The shared shell (web/src/shared/*) imports none of these — composition flows
// one way, from the entry down into the shell's registries.
import { registerCoreDomain } from "@/domains/core/register";
import { registerBooksDomain } from "@/domains/books/register";
import { registerBoomtimeDomain } from "@/domains/boomtime/register";

let done = false;

/** Register every domain the boomtime host app ships. Idempotent — safe to call
 *  from the app entry and from the test setup (the underlying register* calls
 *  are themselves idempotent, and this guard skips repeat work). */
export function registerHostDomains(): void {
  if (done) return;
  done = true;
  registerCoreDomain();
  registerBooksDomain();
  registerBoomtimeDomain();
}
