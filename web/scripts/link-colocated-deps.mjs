// link-colocated-deps.mjs — make the colocated domain FE trees resolvable.
//
// THE PROBLEM. internal/books/web/src and internal/boomtime/web/src hold the
// books and boomtime domain frontends, physically colocated with their Go
// packages and reached from the Vite root via the @books / @boomtime aliases.
// They import ordinary packages (react, lucide-react, @tanstack/react-query, …)
// declared in web/package.json — but Node resolves a bare specifier by walking
// up from the IMPORTER, and walking up from internal/books/web/src never reaches
// web/node_modules.
//
// Those imports only ever worked because a developer checkout sits inside the
// catalyst-devspace monorepo, whose hoisted node_modules happens to carry the
// same packages. In CI — a bare clone with deps installed only under web/ —
// there is nothing to hoist from, so vitest collapsed on "Failed to resolve
// import lucide-react" and tsc emitted 738 implicit-any errors from types it
// could not find.
//
// THE FIX. Symlink a node_modules into each colocated tree, pointing at the one
// under web/. Ordinary Node resolution then succeeds from those files for every
// tool at once — tsc, vite, vitest, eslint — instead of each needing its own
// config workaround. Vite's resolve.dedupe covers the bundler but tsc does not
// read it, and a tsconfig `paths` catch-all is actively worse: mapping "*" to
// ./node_modules/* bypasses @types lookup, taking the error count from 738 to
// 7183.
//
// Runs from web/postinstall so it is re-established after every install, rather
// than committed as a symlink that would dangle in a fresh clone.
//
// Deliberately never fails the install: a Docker stage that copies only web/ has
// no colocated trees to link, and that is fine — there is simply nothing to do.
import { existsSync, lstatSync, symlinkSync, unlinkSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const target = join(webDir, "node_modules");

// Keep in sync with the @books / @boomtime aliases in vite.config.ts and the
// matching paths in tsconfig.app.json.
const colocated = ["../internal/books/web", "../internal/boomtime/web"];

function isSymlink(p) {
  try {
    return lstatSync(p).isSymbolicLink();
  } catch {
    return false;
  }
}

if (!existsSync(target)) {
  console.warn("[link-colocated-deps] web/node_modules is absent; nothing to link");
  process.exit(0);
}

for (const rel of colocated) {
  const treeDir = resolve(webDir, rel);
  if (!existsSync(treeDir)) continue; // e.g. a Docker stage that copied only web/
  const link = join(treeDir, "node_modules");
  try {
    if (isSymlink(link)) {
      unlinkSync(link); // replace a stale or dangling link
    } else if (existsSync(link)) {
      // A REAL directory is someone's deliberate install; clobbering it would
      // be destructive, so leave it and say so.
      console.warn(`[link-colocated-deps] ${rel}/node_modules is a real directory, leaving it alone`);
      continue;
    }
    symlinkSync(relative(treeDir, target), link, "junction");
    console.log(`[link-colocated-deps] linked ${rel}/node_modules -> web/node_modules`);
  } catch (err) {
    console.warn(`[link-colocated-deps] could not link ${rel}: ${err.message}`);
  }
}
