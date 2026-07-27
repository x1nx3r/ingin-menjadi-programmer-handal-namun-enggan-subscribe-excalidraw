# syntax=docker/dockerfile:1
# ─────────────────────────────────────────────────────────────────────────────
# IMPHISE — Excalidraw wrapper. Single binary, SQLite, WebSockets, no runtime.
#
# Build:  docker build -t gotth . --provenance=false
# Run:    docker run -p 3000:3000 \
#           -e SUPER_ADMIN_EMAIL=you@example.com \
#           -v gotth-data:/data \
#           -v /path/to/creds.json:/app/service-account.json:ro \
#           gotth
#
# Required:
#   SUPER_ADMIN_EMAIL          env var. No default.
#   Firebase service account   mount into /app (auto-discovered) or set
#                              FIREBASE_CREDENTIALS=/path/to/file.json.
# Optional:
#   PORT            (default 3000)
#   SQLITE_DB_PATH  (default /data/canvas.db)
# ─────────────────────────────────────────────────────────────────────────────

# ─── Stage 1: Excalidraw bundle + fonts ──────────────────────────────────────
FROM node:22-bookworm-slim AS assets
WORKDIR /build

COPY app/assets/excalidraw/package.json app/assets/excalidraw/package-lock.json ./
RUN npm ci

COPY app/assets/excalidraw/entry.js ./
RUN mkdir -p /out \
    && npx --yes esbuild@0.25 entry.js \
       --bundle --outfile=/out/excalidraw.bundle.js --minify \
       --format=iife --global-name=ExcalidrawBundle \
       --define:process.env.NODE_ENV='"production"' \
    && cp -r node_modules/@excalidraw/excalidraw/dist/prod/fonts /out/fonts

# ─── Stage 2: templ + Tailwind + CGO build (Alpine / musl) ──────────────────
# Alpine so the binary links against musl — enables a ~7 MB runtime image.
FROM golang:1.25-alpine AS build

ARG TEMPL_VERSION=v0.3.1020
ARG TAILWIND_VERSION=4.3.2

# gcc + musl-dev for sqlite3 CGO. nodejs + npm because Tailwind's standalone
# binary is glibc-only, but its npm package CLI runs anywhere Node does.
# binutils gives us 'strip' to shave CGO debug symbols that -s -w misses.
RUN apk add --no-cache gcc musl-dev nodejs npm binutils

RUN go install github.com/a-h/templ/cmd/templ@${TEMPL_VERSION}

# Tailwind v4 CLI is a separate npm package; the core 'tailwindcss' package
# is needed for @import resolution in globals.css.
RUN npm install -g tailwindcss@${TAILWIND_VERSION} @tailwindcss/cli@${TAILWIND_VERSION}

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=assets /out/excalidraw.bundle.js app/assets/public/excalidraw.bundle.js
COPY --from=assets /out/fonts app/assets/public/fonts

RUN templ generate \
    && npm install --no-save tailwindcss@${TAILWIND_VERSION} \
    && tailwindcss -i app/globals.css -o app/assets/globals.css.output --minify \
    && go test ./... \
    && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/server .

# Strip C/C++ symbols that Go's -s doesn't touch (sqlite3 binding, etc).
RUN strip -s /out/server

# ─── Stage 3: Runtime ───────────────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates for Firebase HTTPS. curl for the healthcheck.
RUN apk add --no-cache ca-certificates curl \
    && adduser -D -u 10001 app

COPY --from=build /out/server /usr/local/bin/server

WORKDIR /app
RUN mkdir -p /data && chown app:app /data
VOLUME ["/data"]

USER app
ENV PORT=3000 \
    SQLITE_DB_PATH=/data/canvas.db

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["sh", "-c", "curl -fsS \"http://localhost:${PORT:-3000}/\" >/dev/null || exit 1"]

CMD ["server"]
