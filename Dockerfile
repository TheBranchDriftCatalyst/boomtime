# ── Stage 1: build the React SPA ─────────────────────────────────────────────
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/yarn.lock ./
# --ignore-engines: a transitive dep (@icons-pack/react-simple-icons via
# catalyst-ui) declares node>=24, but nothing in the build actually needs
# node 24 semantics. npm ci ignored engine mismatches silently; yarn 1's
# --frozen-lockfile treats them as fatal, so the pragma stays until we
# bump the base image (or the dep drops the requirement).
RUN yarn install --frozen-lockfile --ignore-engines
COPY web/ ./
# Part B Stage 2: the widget catalog spec is ONE committed file,
# internal/widget/specs.json, consumed by BOTH the Go SVG renderer (//go:embed)
# and the FE (imported via the "@widget-specs" alias → ../internal/widget/specs.json,
# see web/vite.config.ts + web/tsconfig.app.json). This web stage only copies
# web/, so the alias would fail to resolve here (it works locally because the
# whole repo is present). Copy the single source into the aliased path so tsc +
# vite resolve it — no duplicate file, still one source of truth.
COPY internal/boomtime/widget/specs.json /internal/boomtime/widget/specs.json
# gaka-zp2s / gaka-abg0 Step B: the books domain FE is PHYSICALLY colocated with
# its Go package under internal/books/web/src (outside /web), reached via the
# `@books/*` alias (web/vite.config.ts + web/tsconfig.app.json) and scanned for
# Tailwind classes via an @source in web/src/index.css. The host build imports
# it (registerBooksDomain), so bring the source in at the aliased path — same
# no-duplicate, single-source-of-truth trick as the widget spec above. tsc
# resolves bare deps (react, vitest, …) by walking up from each file, so also
# link the colocated root's node_modules at /web/node_modules.
COPY internal/books/web/src /internal/books/web/src
RUN ln -s /web/node_modules /internal/books/web/node_modules
RUN yarn build

# ── Stage 2: build the Go binary with the SPA embedded ───────────────────────
# VERSION / BRANCH / COMMIT / BUILDTIME are not secrets — they're build
# metadata stamped into the binary via ldflags and surfaced by /healthz.
# Passed by CI (release.yml) or `task docker:build`. Safe defaults for a bare
# local build. NEVER add secret build args here. All BOOM_* / WAKATIME_API_KEY
# / GITHUB_TOKEN stay as RUNTIME env, injected via docker run -e / compose
# env_file.
FROM golang:1.25-alpine AS server
ARG VERSION=dev
ARG BRANCH=""
ARG COMMIT=""
ARG BUILDTIME=""
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
# COPY . . picks up the whole build context — .dockerignore is the sole guard
# that secrets/junk don't leak into builder layers (auditable via docker
# history --no-trunc on a --target=server build).
COPY . .
# Embed the built SPA (server package embeds internal/shared/server/dist).
COPY --from=web /web/dist ./internal/shared/server/dist
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.branch=${BRANCH} -X main.commit=${COMMIT} -X main.buildTime=${BUILDTIME}" \
    -o /out/boomtime ./cmd/boomtime

# ── Stage 3: minimal runtime ─────────────────────────────────────────────────
FROM alpine:3.20
ARG VERSION=dev
# OCI labels — GHCR uses `image.source` to link the package to the repo.
LABEL org.opencontainers.image.title="boomtime" \
      org.opencontainers.image.description="Wakatime-compatible coding-time tracker (Go + React)." \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/TheBranchDriftCatalyst/boomtime" \
      org.opencontainers.image.licenses="Unlicense"
RUN apk add --no-cache ca-certificates tzdata bash bash-completion && adduser -D -u 10001 boomtime
COPY --from=server /out/boomtime /usr/local/bin/boomtime
# Bake shell completions into the image at build time (gaka-0oe.10). Generation
# is offline (no DB) and runs in THIS stage so the binary executes on the target
# platform — generating in the builder would break cross-arch (buildx) images.
# Static command/role completion works out of the box; dynamic entity
# completion (usernames from the DB) connects at runtime when you TAB.
RUN mkdir -p /usr/share/bash-completion/completions /usr/share/zsh/site-functions /usr/share/fish/vendor_completions.d \
    && boomtime completion bash > /usr/share/bash-completion/completions/boomtime \
    && boomtime completion zsh  > /usr/share/zsh/site-functions/_boomtime \
    && boomtime completion fish > /usr/share/fish/vendor_completions.d/boomtime.fish
USER boomtime
ENV BOOM_PORT=8080
EXPOSE 8080
# `run` applies migrations then serves (and starts the import worker).
ENTRYPOINT ["boomtime"]
CMD ["run"]
