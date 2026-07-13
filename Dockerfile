# ── Stage 1: build the React SPA ─────────────────────────────────────────────
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci || npm install
COPY web/ ./
RUN npm run build

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
# Embed the built SPA (server package embeds internal/server/dist).
COPY --from=web /web/dist ./internal/server/dist
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
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 boomtime
COPY --from=server /out/boomtime /usr/local/bin/boomtime
USER boomtime
ENV BOOM_PORT=8080
EXPOSE 8080
# `run` applies migrations then serves (and starts the import worker).
ENTRYPOINT ["boomtime"]
CMD ["run"]
