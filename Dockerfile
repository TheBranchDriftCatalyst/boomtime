# ── Stage 1: build the React SPA ─────────────────────────────────────────────
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci || npm install
COPY web/ ./
RUN npm run build

# ── Stage 2: build the Go binary with the SPA embedded ───────────────────────
FROM golang:1.25-alpine AS server
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# Embed the built SPA (server package embeds internal/server/dist).
COPY --from=web /web/dist ./internal/server/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/gakatime ./cmd/gakatime

# ── Stage 3: minimal runtime ─────────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 gakatime
COPY --from=server /out/gakatime /usr/local/bin/gakatime
USER gakatime
ENV HAKA_PORT=8080
EXPOSE 8080
# `run` applies migrations then serves (and starts the import worker).
ENTRYPOINT ["gakatime"]
CMD ["run"]
