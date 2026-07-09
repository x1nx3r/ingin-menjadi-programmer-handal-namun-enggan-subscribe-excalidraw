# a-next-lie/gotth-app/Makefile
# ──────────────────────────────────────────────────────
# Backed by a compilation directory (gotth_build).
# ──────────────────────────────────────────────────────

TAILWIND_VERSION := v4.3.2
TAILWIND_BIN     := ./bin/tailwindcss
APP_BIN          := ./bin/server

UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

ifeq ($(UNAME_S),Linux)
  ifeq ($(UNAME_M),x86_64)
    TAILWIND_PLATFORM := linux-x64
  else ifeq ($(UNAME_M),aarch64)
    TAILWIND_PLATFORM := linux-arm64
  endif
else ifeq ($(UNAME_S),Darwin)
  ifeq ($(UNAME_M),arm64)
    TAILWIND_PLATFORM := macos-arm64
  else
    TAILWIND_PLATFORM := macos-x64
  endif
endif

TAILWIND_URL := https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$(TAILWIND_PLATFORM)

.PHONY: help setup dev build start clean templ css sync

help:
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':'

## setup: Install Go tools and standalone Tailwind binary
setup: $(TAILWIND_BIN)
	@which templ > /dev/null || (echo "Installing templ..." && go install github.com/a-h/templ/cmd/templ@latest)
	@which air > /dev/null || (echo "Installing air..." && go install github.com/air-verse/air@latest)
	@$(MAKE) sync
	@cd gotth_build && templ generate
	go mod tidy

$(TAILWIND_BIN):
	@mkdir -p bin
	curl -L --progress-bar -o $(TAILWIND_BIN) $(TAILWIND_URL)
	chmod +x $(TAILWIND_BIN)

TEMPL_BIN := $(shell which templ 2>/dev/null || echo "$(shell go env GOPATH)/bin/templ")

## templ: Generate Go files in-place
templ:
	@which templ > /dev/null || (echo "Installing templ..." && go install github.com/a-h/templ/cmd/templ@latest)
	@$(TEMPL_BIN) generate

## css: Compile Tailwind CSS (scans root source templ files)
css:
	@which $(TAILWIND_BIN) > /dev/null && $(TAILWIND_BIN) -i app/globals.css -o app/assets/globals.css.output --minify || npx @tailwindcss/cli -i app/globals.css -o app/assets/globals.css.output --minify

## dev: Run live-reloading dev server
dev: $(TAILWIND_BIN) bundle
	@$(MAKE) css
	@bash -c 'trap "kill 0" EXIT; $(TAILWIND_BIN) -i app/globals.css -o app/assets/globals.css.output --watch & air; wait'

## bundle: Bundle Excalidraw with esbuild
bundle: app/assets/excalidraw/node_modules
	npx esbuild app/assets/excalidraw/entry.js --bundle --outfile=app/assets/public/excalidraw.bundle.js --minify --format=iife --global-name=ExcalidrawBundle

app/assets/excalidraw/node_modules: app/assets/excalidraw/package.json
	cd app/assets/excalidraw && npm install

## build: Compile production server binary and assets
build: css templ bundle
	go build -o $(APP_BIN) main.go
