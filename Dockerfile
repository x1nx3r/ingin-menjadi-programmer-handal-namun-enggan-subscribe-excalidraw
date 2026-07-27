# syntax=docker/dockerfile:1
# ─────────────────────────────────────────────────────────────────────────────
# IMPHISE — Excalidraw wrapper. Single binary, SQLite, WebSockets, no runtime.
#
# Build:  docker build -t gotth . --provenance=false
#
# Cache architecture:
#   • Toolchain (apk/templ/tailwind) → never changes unless ARGs change
#   • go mod download                 → only when go.mod/go.sum change
#   • CSS pipeline (templ+tailwind)   → only when .templ or globals.css change
#   • Go build (with build cache)     → incremental after any source change
# ─────────────────────────────────────────────────────────────────────────────

# ─── Stage 1: Excalidraw bundle + fonts ──────────────────────────────────────
FROM node:22-bookworm-slim AS assets
WORKDIR /build

COPY app/assets/excalidraw/package.json app/assets/excalidraw/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

COPY app/assets/excalidraw/entry.js ./
RUN mkdir -p /out \
    && npx --yes esbuild@0.25 entry.js \
       --bundle --outfile=/out/excalidraw.bundle.js --minify \
       --format=iife --global-name=ExcalidrawBundle \
       --define:process.env.NODE_ENV='"production"' \
    && cp -r node_modules/@excalidraw/excalidraw/dist/prod/fonts /out/fonts

# ─── Stage 2: Build (Alpine / musl) ─────────────────────────────────────────
FROM golang:1.25-alpine AS build

ARG TEMPL_VERSION=v0.3.1020
ARG TAILWIND_VERSION=4.3.2

# ── Toolchain (effectively permanent cache hits) ─────────────────────────
RUN apk add --no-cache gcc musl-dev nodejs npm binutils
RUN go install github.com/a-h/templ/cmd/templ@${TEMPL_VERSION}

# Both packages needed: @tailwindcss/cli for the 'tailwindcss' command,
# tailwindcss (core) for @import resolution in globals.css.
RUN npm install -g tailwindcss@${TAILWIND_VERSION} @tailwindcss/cli@${TAILWIND_VERSION}

WORKDIR /src

# ── Module cache (invalidates only on go.mod/go.sum) ─────────────────────
COPY go.mod go.sum ./
RUN go mod download

# ── CSS pipeline (invalidates only on .templ or globals.css changes) ─────
# Files are copied with exact globs so a Go-handler-only change skips this
# entire layer. The generated *_templ.go and globals.css.output survive the
# later COPY . . because they are git-ignored (absent from build context).
COPY app/globals.css              app/globals.css
COPY app/*.templ                  app/
COPY app/admin/*.templ            app/admin/
COPY app/canvas/*.templ           app/canvas/
COPY app/components/*.templ       app/components/
COPY app/dashboard/*.templ        app/dashboard/
COPY app/profile/*.templ          app/profile/

RUN mkdir -p node_modules \
    && ln -sf /usr/local/lib/node_modules/tailwindcss node_modules/tailwindcss \
    && find app -name '*_templ.go' -delete \
    && templ generate \
    && tailwindcss -i app/globals.css -o app/assets/globals.css.output --minify

# ── Full source + Go build (re-runs on any file change; cache mount makes
#    incremental compiles ~10 s instead of ~90 s) ─────────────────────────
COPY . .
COPY --from=assets /out/excalidraw.bundle.js app/assets/public/excalidraw.bundle.js
COPY --from=assets /out/fonts app/assets/public/fonts

RUN --mount=type=cache,target=/root/.cache/go-build \
    go test ./... \
    && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/server .

# Strip C/C++ symbols that Go's -s doesn't touch (sqlite3 binding).
RUN strip -s /out/server

# ─── Stage 3: Runtime ───────────────────────────────────────────────────────
FROM alpine:3.21

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
