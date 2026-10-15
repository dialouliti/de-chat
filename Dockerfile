# syntax=docker/dockerfile:1

# --- Build stage ---
FROM golang:1.21-alpine AS build
WORKDIR /src

# Cache deps first.
COPY go.mod go.sum* ./
RUN go mod download

# Build the rest of the source.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out/dechat ./cmd/server

# Dump the OpenAPI spec — the web stage generates its typed API client from
# it (cmd/openapi writes openapi.json into the working directory).
RUN CGO_ENABLED=0 go run ./cmd/openapi

# --- Web build stage (PWA) ---
FROM node:24-alpine AS web
# Pin pnpm 11: it accepts the settings-only web/pnpm-workspace.yaml (no
# `packages` field — pnpm < 10.5 rejects such a manifest) and reads the
# esbuild build approval from it. pnpm 11 needs node >= 22.13.
RUN npm install -g pnpm@11.28.2
WORKDIR /src/web

# Cache deps first (pnpm-workspace.yaml carries the esbuild build approval).
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

# Sources + the freshly generated spec → typed client + production build.
COPY web/ ./
COPY --from=build /src/openapi.json /src/openapi.json
RUN pnpm gen:api && pnpm build

# --- Runtime stage (compose target `runtime`) ---
FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 dechat
USER dechat
COPY --from=build /out/dechat /usr/local/bin/dechat

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/dechat"]

# --- Caddy edge stage (compose target `caddy`) ---
# Terminates TLS, proxies API + wss to app:8080, serves the PWA from /srv.
# The image's default ENTRYPOINT/CMD pick up /etc/caddy/Caddyfile.
FROM caddy:2.10-alpine AS caddy
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=web /src/web/dist /srv
