# Repo-shared git hooks

These hooks catch build/type drift locally before it reaches CI. Committed to the repo so every clone gets the same guards — but you have to opt in once per clone.

## Activate

```bash
git config core.hooksPath .githooks
```

Run once after cloning. Persists in `.git/config`.

## What's here

- **`pre-push`** — runs `cd web && npm run build` (identical to Docker CI stage 1). Blocks the push if the TypeScript build or Vite bundle fails. Rationale: during the catalyst-ui migration we hit multiple rounds of "works locally with yarn-link, fails in Docker with npm install" from @types/react version drift and Radix forwardRef typing quirks. This closes the loop.

## Bypass once

If you really need to push around a build failure (unusual):

```bash
git push --no-verify
```

## Related

- `catalyst-ui` has an analogous `.husky/pre-push` that runs `yarn build` before any push, so the shared library can't ship a version that consumer builds would reject.
