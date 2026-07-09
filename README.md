<p align="center">
  <img src="app/assets/public/logo.svg" width="96" height="96" alt="IMPHISE" style="border: 2px solid var(--border); padding: 8px;" />
</p>

<h1 align="center">Ingin Menjadi Programmer Handal Namun Enggan Subscribe Excalidraw</h1>

<p align="center">
  <strong>Live: <a href="https://canvas.x1nx3r.dev" target="_blank">canvas.x1nx3r.dev</a></strong>
</p>

Draw boxes. Connect them. Call it architecture.

An Excalidraw wrapper that actually works. No SaaS emails, no subscription
to unlock the felt-tip pen, no "upgrade to pro to export as PNG." Just draw.

**IMPHISE** is a Go + Templ + HTMX + Tailwind v4 app that wraps Excalidraw,
saves to SQLite, shares via links, and runs as a single binary on a $5 VPS.

## Stack

| Layer | Choice | Reason |
|---|---|---|
| Language | Go | Compiles to a binary. No runtime. No `node_modules`. |
| Templates | Templ | Type-safe HTML that doesn't make you want to cry. |
| CSS | Tailwind v4 (standalone) | Utility classes. Custom CSS generation for responsive variants. |
| Interactivity | HTMX | Server-rendered HTML fragments. No virtual DOM. |
| Auth | Firebase Auth Web SDK | Google sign-in. ID tokens. Server-verified session cookies. |
| Database | SQLite (`mattn/go-sqlite3`) | Single file. WAL mode. Zero config. CGO required. |
| Canvas | Excalidraw 0.18.1 (bundled) | 8MB of someone else's hard work. I just render it. |

## Getting Started

```bash
# Prerequisites: Go 1.22+, Node (for the excalidraw bundle), CGO (for SQLite)
make setup     # installs templ, tailwind, downloads deps
make dev       # live-reloading dev server on :3000
```

Or just:

```bash
go run .
```

Open [http://localhost:3000](http://localhost:3000). You'll see a landing
page. Click "Start Drawing." Sign in with Google. Draw a box. It saves
automatically because I didn't want you to lose your masterpiece.

**Note:** Requires `CGO_ENABLED=1` because of `mattn/go-sqlite3`. This is
the only dependency that needs CGO. On macOS it just works. On Linux you
might need `gcc` installed (it's usually there already). On the server,
`deploy.sh` sets `CGO_ENABLED=1 GOOS=linux GOARCH=amd64` for cross-compilation.

## How It Works

### Auth Flow

1. User clicks "Sign in with Google."
2. Firebase Auth Web SDK opens a popup, user authenticates.
3. Client sends the ID token to `POST /auth/login`.
4. Server verifies the token with the Firebase Admin SDK and creates an
   http-only session cookie (`14 * 24 * time.Hour` expiry, `SameSite=Strict`).
5. Middleware verifies the cookie on every request and injects the user's
   `uid` into the request context.

That's it. No JWT parsing on the client. No `localStorage` tokens. No
"refresh token rotation" blog post you'll read at 2AM and immediately forget.

Login redirects to `/drawings`, logout redirects to `/`. Both handled via
HTMX `HX-Redirect` headers — the form `POST` triggers a full page navigation
without any JavaScript on the client side.

### Canvas Save/Load

Excalidraw fires an `onChange` callback on every user action. The client
debounces this with a 2-second timer. When the timer fires, it sends the
entire scene (elements + appState) to `POST /api/draw/{id}/save`. The
server writes it to SQLite as a JSON blob in the `content` column.

Loading is the reverse: `GET /api/draw/{id}/data` reads from SQLite,
sanitizes the `appState.collaborators` field (because Excalidraw crashes
if that's not an array — ask me how I know), and returns the scene.

### SQLite

The database is a single `canvas.db` file with WAL mode, busy timeout of
5 seconds, and foreign keys enabled. The schema:

```sql
CREATE TABLE drawings (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT 'Untitled',
    content TEXT,
    share_slug TEXT UNIQUE,
    thumbnail TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_drawings_owner ON drawings(owner_id);
CREATE INDEX idx_drawings_share_slug ON drawings(share_slug);
```

`content` stores the full Excalidraw scene as JSON. `thumbnail` stores a
base64-encoded PNG (under 100KB). `share_slug` is nullable — only set when
the drawing is shared. The app uses `MaxOpenConns(1)` because SQLite doesn't
handle concurrent writers well, and WAL mode + busy timeout handles the rest.

On the server, the database lives in `/var/www/udin-canvas/shared/canvas.db`
and is symlinked into each release directory. This means deploys never
touch the database — it persists across atomic symlink swaps.

### Excalidraw Bundle

Excalidraw is bundled at build time via esbuild:

```bash
npx esbuild app/assets/excalidraw/entry.js \
  --bundle \
  --outfile=app/assets/public/excalidraw.bundle.js \
  --minify \
  --format=iife \
  --global-name=ExcalidrawBundle
```

The entry point exports `Excalidraw`, `React`, `ReactDOM`, and
`exportToBlob` as globals. The output is ~8MB. It lives in the binary via
`//go:embed`. The CSS is a separate file (also embedded) that gets linked
in the page head.

Versions are pinned to exact (`@excalidraw/excalidraw: 0.18.1`,
`react: 18.3.1`, `react-dom: 18.3.1`) because Excalidraw's CSS classes
change between versions, and our brutalist override stylesheet depends on
specific selectors.

### Sharing

Clicking "Share" hits `POST /api/draw/{id}/share`, which generates a
16-byte random hex slug, stores it on the drawing, and returns it. The
share dialog shows a read-only URL (`/shared/{slug}`). Anyone with that
URL can view the drawing — no auth required.

The shared page uses Excalidraw in `viewModeEnabled: true`. It loads the
scene from `GET /api/shared/{slug}/data`, which does a public query
(no auth middleware) by matching the `share_slug` column.

### Thumbnails

After every save, the client calls `exportToBlob` (exported from the
Excalidraw bundle) to generate a PNG thumbnail. If the blob is under
100KB, it gets base64-encoded and stored in the `thumbnail` column. The
dashboard shows these thumbnails in a grid. If there's no thumbnail, you
get a placeholder with the drawing title.

### CSS Generation (Tailwind v4 Responsive Classes)

Tailwind v4's `@source` directive doesn't scan `.templ` files by default.
This means responsive variants like `sm:hidden`, `md:flex`, `lg:grid-cols-3`
silently disappear from the output. No warnings. No errors. Just broken
responsive layouts on mobile. For hours.

The fix: a custom Go tool (`tools/generate_css/main.go`) that:

1. Scans all `.templ` files in `app/`
2. Extracts CSS classes matching responsive patterns (`sm:`, `md:`, `lg:`, `xl:`, `dark:`)
3. Generates `app/_entry.css` with `@source inline("...")` declarations
4. Appends the contents of `globals.css`

This gets fed to `npx @tailwindcss/cli` which now sees all responsive
classes. The `make css` target runs this pipeline.

```bash
make css   # generate-css → tailwind → globals.css.output
```

The `globals.css.output` is embedded into the Go binary via `//go:embed`
and served with cache-busting ETags (SHA256 hash).

### CSS Cache Busting

The embedded CSS is served with:
- `Cache-Control: no-cache, must-revalidate`
- `ETag: "<sha256-hash>"` computed at init time

Cloudflare aggressively caches static assets. The `no-cache` directive
was being ignored by Cloudflare's CDN layer, causing stale CSS to be
served. The ETag at least lets browsers revalidate properly.

If you deploy and the spacing looks wrong, purge Cloudflare's cache.
This is not a bug. This is Cloudflare being Cloudflare.

### Excalidraw Theming

Excalidraw's default CSS has rounded corners, soft shadows, and a color
scheme that doesn't match anything. I wrote an override stylesheet
(`excalidraw-brutalist.css`) that maps Excalidraw's internal CSS variables
to our design tokens, sets `border-radius: 0` everywhere, replaces soft
shadows with hard offset shadows (`4px 4px 0px`), and applies 2px borders
to everything that stands still long enough.

The theme toggle syncs with Excalidraw's internal state. When you flip
from Latte to Mocha, the canvas follows without needing a page reload.

The canvas background uses Catppuccin's `--ctp-base` color instead of
white, so the canvas feels like part of the app rather than a foreign
iframe.

### PWA Manifest

A `manifest.json` is embedded and served with the correct `Content-Type`.
Layout templates include the `<meta>` tags for theme-color and
apple-mobile-web-app-capable. No service worker yet — that's a problem
for future me.

## Project Structure

```
main.go                          # Routes, middleware, static serving
app/
  lib/                           # Shared infrastructure
    auth.go                      # Firebase Admin SDK init
    middleware.go                # Session cookie verification, RequireAuth
    auth_handlers.go             # Login/logout/user + navBtnClass
    db.go                        # SQLite init, WAL mode, schema migration
  api/                           # API handlers (no HTML rendering)
    draw.go                      # Data, Save, Share, Rename, Thumbnail, Delete
    shared.go                    # Public shared data handler (no auth)
  canvas/                        # Canvas pages (Excalidraw editor)
    page.templ                   # Canvas editor with responsive title bar
    shared.templ                 # Read-only shared view
  dashboard/
    page.templ                   # Drawing list + new drawing
  profile/
    page.templ                   # User profile with drawing grid
  components/
    navigation.templ             # Hamburger menu (mobile) + desktop nav
    logo.templ                   # Icon-only on mobile, text on desktop
    drawing_card.templ           # Card with thumbnail, rename, delete
    footer.templ                 # GOTTH badge + IMPHISE copyright
    empty_state.templ            # Empty state for dashboard
  layout.templ                   # Root HTML shell (Bungee + Space Mono, Firebase, PWA)
  canvas_layout.templ            # Minimal shell for Excalidraw pages
  page.templ                     # Landing page (hero, features, CTA)
  globals.css                    # Catppuccin tokens, Tailwind, Bungee + Space Mono
  assets/
    excalidraw/                  # Excalidraw source (entry.js, package.json)
    public/                      # Static files (logo, bundle, brutalist CSS, manifest)
    assets.go                    # go:embed directives + CSSHash
  _entry.css                     # Generated: @source inline() + globals.css
tools/
  generate_css/main.go           # Scan templ → extract responsive classes → _entry.css
```

### Layout Unification

All pages use one of two layout templates:

- **`Layout`** — Full HTML shell with nav, footer, Firebase SDK, HTMX. Used
  by landing, dashboard, and profile pages.

- **`CanvasLayout`** — Minimal HTML shell for Excalidraw pages (just the head
  with fonts, theme init, PWA meta tags).

### Responsive Design

Every page adapts to mobile:

- **Navigation**: Hamburger menu on mobile (`md:hidden`/`md:flex`), full nav on desktop
- **Logo**: Icon-only on mobile, text on `sm:` and up
- **Auth bar**: Returns both `#auth-bar` (desktop) and `#auth-bar-mobile` (mobile)
  via `hx-swap-oob="true"` — mobile gets stacked layout with full-width buttons
- **Landing page**: Single column on mobile, multi-column on `sm:`/`lg:`
- **Dashboard**: Stacked cards on mobile, grid on `sm:grid-cols-2`
- **Canvas title bar**: Logo hidden on mobile, input shrinks, links collapse
- **Decorative elements**: Hidden on mobile, visible on `md:` and up

### Auth Bar

The nav auth bar is loaded via HTMX on every page. The server returns
consistent HTML with a shared brutalist button class defined once in
`auth_handlers.go` as `navBtnClass`:

```
px-3 py-1.5 border-2 border-[var(--accent)] bg-[var(--accent)]
text-[var(--accent-fg)] text-xs font-bold uppercase tracking-wider
cursor-pointer hover:bg-[var(--mauve)] transition-all
active:translate-x-0.5 active:translate-y-0.5 active:shadow-none
shadow-[2px_2px_0px_0px_var(--accent)]
```

## Deployment

The app deploys as a single binary to a bare-metal VPS via atomic symlink swap.

### The Flow

```bash
bash deploy.sh
```

1. **Local build:** `make css && make templ && CGO_ENABLED=1 go build`
2. **rsync:** Binary + Firebase JSON + Makefile.server → timestamped release dir
3. **Atomic swap:** `ln -nfs` points `current` → new release
4. **Symlink DB:** SQLite files in `shared/` are symlinked into the new release
5. **Restart:** `systemd restart udin-canvas`
6. **Clean up:** Old releases compressed to `.tar.xz` (keep last 5)

Zero downtime. Binary is ~61MB, DB is a single file, deploy takes ~50s.

### Server Setup

- **Reverse proxy:** Caddy → app port
- **Service:** systemd unit with memory limit
- **Database:** shared SQLite file (persists across deploys via symlinks)
- **Peak memory:** ~16MB
- **CDN:** Cloudflare (aggressive caching, purge when CSS changes)

### Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `PORT` | No | `3000` | HTTP listen port |
| `SQLITE_DB_PATH` | No | `./canvas.db` | Path to SQLite database file |

The Firebase service account JSON is auto-detected by scanning for
`-firebase-adminsdk-*.json` in the working directory.

## The Suffering Log

Things that went wrong during development, in roughly chronological order:

1. **Firestore → SQLite migration** — Moved from a managed service to a
   single file. Had to rewrite every query handler. Worth it.

2. **Login/logout routing** — The login handler set `HX-Redirect: /drawings`
   but the logout handler used `HX-Redirect: /`. Both work. Neither was
   obvious. The logout form also had `hx-boost="false"` which was silently
   swallowing the `HX-Redirect` header. Removed it. Fixed.

3. **Excalidraw canvas text font** — Set `body { font-family: 'Space Mono' }`
   in globals.css. This leaked into Excalidraw's canvas text input. Added
   a CSS reset in `excalidraw-brutalist.css` targeting `.excalidraw .text-editor`.
   Still persists. Accepted as a minor issue.

4. **Double branding** — Logo component was rendering in both the navigation
   AND the landing page hero section. Removed it from the landing page.
   Logo now only appears in nav and canvas pages.

5. **Tailwind v4 responsive classes silently disappearing** — `sm:hidden`,
   `md:flex`, `lg:grid-cols-3` were all missing from the CSS output. No
   warnings. No errors. Just broken layouts. Spent hours debugging before
   discovering that Tailwind v4's `@source` doesn't scan `.templ` files.
   Built a Go tool to fix it.

6. **`@source inline()` not working** — Thought `@source inline("sm:hidden md:flex")`
   would tell Tailwind to generate those classes. It doesn't. The classes
   must appear in the source files Tailwind scans. The fix: include the
   actual `.templ` content via `@source inline()` with the full class list,
   or concatenate the templ file contents as CSS comments.

7. **Cloudflare caching stale CSS** — Deployed new CSS, still serving old
   version. The `Cache-Control: no-cache` header was being overwritten by
   Cloudflare's CDN layer. Had to add ETag-based cache busting and still
   need manual cache purge on deploy.

8. **Font choices** — Started with Cinzel (headings) + Inter (body) +
   Fira Code (mono). Changed to Bungee (headings) + Space Mono (body/UI).
   Had to update every template, layout, and CSS file. Then the Space Mono
   leaked into Excalidraw. The font saga continues.

9. **Mobile nav** — Built a hamburger menu with vanilla JS (`toggleMobileMenu()`).
   Used `md:hidden`/`md:flex` for responsive visibility. Then discovered the
   responsive classes weren't being generated. See item 5.

10. **Auth bar OOB swap** — `UserHandler` returns HTML for both `#auth-bar`
    (desktop) and `#auth-bar-mobile` (mobile) with `hx-swap-oob="true"`.
    The mobile version has a stacked layout with full-width logout button.
    This required changing the handler to return both elements in a single
    response, which HTMX then surgically inserts into the DOM.

## Design System

**Catppuccin Brutalist** — sharp corners, hard offset shadows, Catppuccin palette.

- **Fonts:** Bungee (headings), Space Mono (body/UI)
- **Borders:** 2px solid, always
- **Shadows:** Hard offset (`2px 2px`, `4px 4px`, `6px 6px`, `8px 8px`), no blur
- **Colors:** Catppuccin Latte (light) + Mocha (dark) with extended accent tokens
- **Buttons:** Translate on active (`translate-x-0.5 translate-y-0.5`), shadow disappears
- **Radius:** Zero. Everywhere. No exceptions.
- **Grid:** Subtle colored grid lines (pink/blue light, mauve/lavender dark)

### Color Tokens

| Token | Latte (Light) | Mocha (Dark) | Usage |
|---|---|---|---|
| `--accent` | `#8839ef` | `#cba6f7` | Primary actions |
| `--mauve` | `#8839ef` | `#cba6f7` | Secondary accent |
| `--pink` | `#ea76cb` | `#f5c2e7` | Feature cards, hover states |
| `--peach` | `#fe640b` | `#fab387` | Accent borders |
| `--teal` | `#179299` | `#94e2d5` | Feature cards |
| `--blue` | `#1e66f5` | `#89b4fa` | Grid lines, links |
| `--lavender` | `#7287fd` | `#b4befe` | Grid lines, secondary |

## Load Testing

Ran k6 load tests against the live server. Results at 500 VUs:

| Metric | Value |
|---|---|
| Error rate | 10.76% (all Cloudflare connection resets, 0% application errors) |
| p95 latency | 739ms (Cloudflare overhead, not server) |
| CRUD checks | 100% pass (create, save, load, rename, share, delete) |

The 10% error rate is entirely Cloudflare dropping connections during the
ramp-up, not the server rejecting requests. The Go binary handles 500
concurrent users with zero application-level errors.

## Why Not Just Use Excalidraw's Built-In Save?

Excalidraw has a "save to disk" button and a "load from disk" button. That
works great for personal use. It doesn't work great for "I want to draw a
diagram on my work computer and open it on my laptop without emailing
myself a JSON file."

SQLite is simpler than Firestore. No indexes to manage, no billing surprises,
no "document size limit." A single `canvas.db` file with WAL mode handles
everything I need. And it's backed up with a `cp` command.

## License

Made with spite and too many hours debugging CSS that should have worked.
