<p align="center">
  <img src="app/assets/public/logo.svg" width="96" height="96" alt="IMPHISE" />
</p>

<h1 align="center">Ingin Menjadi Programmer Handal Namun Enggan Subscribe Excalidraw</h1>

<p align="center">
  <strong>Live: <a href="https://canvas.x1nx3r.dev" target="_blank">canvas.x1nx3r.dev</a></strong>
</p>

*NOW WITH MULTIPLAYER!*

Draw boxes. Connect them. Call it architecture.

Yet another Excalidraw wrapper because the original had the audacity to ask for money so I could export a PNG of my stick figures. This one uses SQLite, ships as a single binary, runs on a $5 VPS that's somehow cheaper than your monthly bubble tea habit, and also does real-time collaboration. Usually without crashing.

---

## Stack

| Thing | What we used | Why, though |
|---|---|---|
| Language | Go 1.22+ | Compiles to a screaming fast binary with no runtime. No JVM. No interpreter. No "but it works on my machine." Just a file you `scp` to a server and run. |
| Templates | Templ | Type-safe HTML. Caught three bugs at compile time that would've been silent runtime failures. Which is three more bugs than I would've caught with normal templates. |
| CSS | Tailwind v4 standalone | We write utility classes, it generates a CSS file. We also had to write a custom Go tool to scan `.templ` files because Tailwind decided not to support them natively. I'm not bitter. Okay, I'm a little bitter. |
| Interactivity | HTMX | Server-rendered HTML fragments. No React for the normal parts of the website. Then we had to use React for the canvas anyway because Excalidraw is a React component and you can't exactly `hx-get` your way into an infinite canvas. But for everything else? HTMX. |
| Auth | Firebase Auth | Google sign-in. We verify tokens server-side. http-only cookies. Your password stays between you and Google, which is none of our business. |
| Database | SQLite | One file. Zero config. No "please wait while we provision your database instance." We have a background goroutine that runs `PRAGMA wal_checkpoint(PASSIVE)` every 5 minutes because otherwise the WAL file grows until your VPS runs out of disk space and nobody tells you until `df -h` breaks your heart. |
| Canvas | Excalidraw 0.18.1 | ~8 MB of bundled JavaScript written by people smarter than us. We just mount it and pray. |
| Real-time | gorilla/websocket | No framework. No message broker. No CRDT library. The server is a glorified megaphone — it takes bytes from one person and shouts them at everyone else. That's the whole architecture. |

---

## How It Works

Two modes. Zero user confusion (hopefully).

### Solo Mode (What 99% of Users Experience)

You open a drawing. Nobody else is watching. The server never allocates a WebSocket for you. Your browser runs a dirty-bit loop every 3 seconds:

```
onChange fires → _dirty = true

setInterval(flushIfDirty, 3000)
  → if not dirty: you get a beer
  → POST /api/draw/{id}/save with keepalive
  → on failure: set dirty again (try next tick)

beforeunload
  → navigator.sendBeacon('/api/draw/{id}/save', blob)
  → yes this actually works unlike half the stuff in the Web API
```

1,500 concurrent users. 15 MB Go heap. Zero errors. The bottleneck was Linux deciding to kill us at 128 MB of RAM, not anything in our code.

### Collaboration Mode (The Fancy Part Nobody Uses)

Someone opens your shared link. The server snitches on them. Your browser reluctantly upgrades to WebSocket:

```
 1. Guest opens /shared/{slug}
 2. Guest connects WebSocket immediately (they're eager)
 3. Server pushes an SSE event to your page: "someone joined"
 4. Your page: "Ugh, fine" → opens WebSocket
 5. You send a full SCENE_UPDATE
 6. Both of you now exchange full scenes on every onChange
 7. Last guest leaves → SSE event: "they're gone"
 8. COLLAB_ENDED → WebSocket closes
 9. You go back to HTTP solo mode
10. Nobody is watching you draw ever again
11. It's just you and your boxes
12. Like it should be
```

The WebSocket is a stateless pipe. The server never looks at your JSON. It just copies bytes. Server crash? Reconnect. Full SCENE_UPDATE resyncs. Zero data loss. The only state the server keeps is a convenience cache loaded from SQLite, and if that cache is wrong, the next full SCENE_UPDATE fixes it.

---

## Auth

1. Click "Sign in with Google."
2. Popup. Auth. Token.
3. We verify the token server-side using Firebase Admin SDK.
4. http-only cookie set for 14 days with `SameSite=Strict`.
5. That's it. No JWT parsing on the client. No localStorage tokens that XSS can steal. We're not idiots. Well, not completely.

---

## Persistence

**Loading:** Excalidraw mounts. `excalidrawAPI` fires. Fetch `GET /api/draw/{id}/data`. Call `api.updateScene()`. We sanitize `appState.collaborators` to an empty array first because Excalidraw will segfault — emotionally — if that field isn't an array. Found this one the fun way.

**Saving (solo):** 3-second interval + `sendBeacon` on close. Worst case: you lose 3 seconds of drawing. If your browser crashes or your laptop dies mid-stroke, that's not our problem. You chose to draw in a web browser. You know the risks.

**Saving (collab):** HTTP save disabled. Everything goes through WebSocket as SCENE_UPDATE. The owner's 3-second HTTP timer still runs as a safety net. Yes, we send duplicate saves. No, we won't optimize it. We're too busy.

---

## Wire Protocol

| Type | Direction | Payload | What it does |
|---|---|---|---|
| `SCENE_INIT` | Server → You | `{ elements: [...] }` | "Here's the current scene, don't screw it up" |
| `SCENE_UPDATE` | Both ways | `{ payload: { elements: [...] } }` | "I changed this thing, update yourself" |
| `MOUSE_LOCATION` | Both ways | `{ payload: { pointer: {x,y}, username } }` | "I'm over here" — "stop moving I can see you" |
| `COLLAB_ENDED` | Server → You | `{}` | "You're alone again. Go back to HTTP. Forever." |

We had SCENE_DELTA. We deleted it. The delta reconstruction was buggy. Phantom diffs. Stale element caches. Race conditions. It worked in the load test. It broke in real life. Full SCENE_UPDATE at ~50 KB is fine when there's only 2-3 of you. The OOM apocalypse only happens at 50 people in one room, and with the lazy socket, most people don't even open a WebSocket. Problem solved. By avoiding it. That counts.

---

## SQLite

Single file. WAL mode. 5-second busy timeout with `_busy_timeout=5000` because `database is locked` is the most useless error message in the history of databases. Foreign keys on. `MaxOpenConns(1)` because SQLite is a shy introvert that doesn't like being touched by multiple hands.

```sql
CREATE TABLE drawings (
    id         TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT 'Untitled',
    content    TEXT,
    share_slug TEXT UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE drawing_thumbnails (
    drawing_id TEXT PRIMARY KEY REFERENCES drawings(id) ON DELETE CASCADE,
    data       TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

The WAL checkpoint goroutine exists because we once found the WAL file was 4.2 GB and the server was about to run out of disk. SQLite's auto-checkpoint threshold is 1000 pages. Nobody tells you this. I'm telling you this so you don't have to learn it at 3 AM on a Saturday.

---

## The Excalidraw Bundle

```bash
npx esbuild app/assets/excalidraw/entry.js \
  --bundle \
  --outfile=app/assets/public/excalidraw.bundle.js \
  --minify \
  --format=iife \
  --global-name=ExcalidrawBundle
```

~8 MB. Embedded in the binary via `//go:embed`. Your first deploy takes 50 seconds because of this. Subsequent deploys are faster because rsync only sends the parts that changed. But that first deploy? Go make coffee.

---

## Sharing

`POST /api/draw/{id}/share` → 16-byte hex slug → `/shared/{slug}`. Anyone with the URL can view. No account required. If you enable `allow_public_edits`, they can draw too. Yes, that means strangers can draw on your diagram. Yes, that's the point. No, we won't add approval-based access. Go build it yourself.

---

## Thumbnails

After every save, `exportToBlob` renders a PNG. If it's under 100 KB, it gets stored in base64. The dashboard shows these in a grid. If your drawing is too complex to fit in 100 KB of PNG, congratulations — you actually drew something meaningful. Your thumbnail is a mystery. Like your career path.

---

## CSS Cache Busting

`/globals.css` served with `Cache-Control: public, max-age=31536000, immutable` and an ETag of the SHA256 hash of the file. Links include `?v=<hash>`. Hash changes → URL changes → browser fetches new copy. This took us three iterations to get right. The first two involved Cloudflare caching stale CSS and wondering why our layout was broken in production.

---

## The Load Testing Saga

### Round 1: HTTP Baseline — 1,500 VUs

K6. 1,500 virtual users. 39,756 requests. **0 errors.** Go heap: 15 MB. OS memory: 94 MB. The only bottleneck was Linux's OOM killer at 128 MB.

**Verdict:** HTTP solo mode is effectively free. The server is a very fast POST acceptor.

### Round 2: Full SCENE_UPDATE + WebSocket — 50 VUs

We added WebSocket. 50 VUs in one room. Full SCENE_UPDATE on every onChange.

| Metric | Value |
|---|---|
| MemoryPeak | **134 MB — CRASHED** |
| OOMKills | **1** (systemd said no) |
| ws_errors | 82 (0.68/s) |
| Sessions | 68 (18 reconnects, mostly because the server kept dying) |
| data_sent | 52 MB |

Broadcasting 50 KB frames to 50 connections fills TCP buffers. One slow client → buffer full → DROP → disconnect → reconnect → more goroutines → more memory → systemd says "bye."

**Verdict:** Full SCENE_UPDATE at scale is a self-inflicted DDoS.

### Round 3: SCENE_DELTA — 50 VUs

~1 KB per frame. Client-side delta reconstruction. Every 10th delta sends a full SCENE_UPDATE.

| Metric | Value |
|---|---|
| MemoryPeak | **95.8 MB** (survived!) |
| ws_errors | **3 (0.025/s)** |
| Sessions | **50 (0 reconnects)** |
| data_sent | **7.6 MB** (from 52 MB) |

| Metric | Full SCENE_UPDATE | SCENE_DELTA |
|---|---|---|
| MemoryPeak | 134 MB (kaboom) | **95.8 MB** |
| ws_errors | 82 (0.68/s) | **3 (0.025/s)** |
| data_sent | 52 MB | **7.6 MB** |
| Sessions | 68 (18 reconnects) | **50 (0 reconnects)** |

**Verdict:** SCENE_DELTA works at scale. But the client code was held together with duct tape and prayer.

### Round 4: What We Ship — Full SCENE_UPDATE + Lazy Socket

We deleted SCENE_DELTA. It was too fragile. Phantom diffs. Stale `_lastSentElements`. Race conditions between SCENE_INIT and local state. It passed the load test. It failed in the browser. I hate frontend code.

We went back to full SCENE_UPDATE. But now it only fires when someone is actually watching. Solo users? HTTP. No WebSocket. No OOM. The theoretical problem of 50 broadcasters in one room is academic when 49 of them don't have a socket open.

**Verdict:** The best optimization is not doing the thing at all until you absolutely have to.

---

## Route Map

| Method | Path | What it does |
|---|---|---|
| `GET` | `/` | Landing page. Look at all those boxes. |
| `GET` | `/drawings` | Your drawings. Or the void, if you haven't made any. |
| `POST` | `/draw/new` | Creates a new untitled void for your boxes. |
| `GET` | `/draw/{id}` | The canvas. Go draw. |
| `GET` | `/profile` | Your name, email, and existential dread. |
| `GET` | `/api/draw/{id}/data` | Returns your drawing as JSON. |
| `POST` | `/api/draw/{id}/save` | Saves your drawing. Try not to call this 500 times. |
| `POST` | `/api/draw/{id}/share` | Returns a share link so others can witness your genius. |
| `PUT` | `/api/draw/{id}/rename` | Renames your drawing. "Untitled (42)" wasn't cutting it. |
| `POST` | `/api/draw/{id}/thumbnail` | Saves a tiny PNG of your drawing. |
| `DELETE` | `/api/draw/{id}` | Deletes your drawing. Gone. Reduced to atoms. |
| `GET` | `/shared/{slug}` | View someone else's drawing. Or your own, via a link. |
| `GET` | `/api/shared/{slug}/data` | Drawing data, no auth needed. Yes, it's public. That's the point. |
| `GET` | `/api/draw/{id}/ws` | WebSocket for the owner. |
| `GET` | `/api/shared/{slug}/ws` | WebSocket for guests. |
| `GET` | `/api/draw/{id}/collab-status` | "Is anyone else here?" Returns `{ online: N }`. |
| `GET` | `/api/ws/stats` | Hub dump. For when you want to see how many WebSockets are bleeding your RAM. |
| `POST` | `/auth/login` | Sets a session cookie. |
| `POST` | `/auth/logout` | Destroys the session cookie. No takebacks. |
| `GET` | `/auth/user` | Auth bar HTML. |
| `GET` | `/admin/*` | Admin panel. You can't see it. Don't try. |

---

## Project Structure

```
main.go                        # Where the magic starts. And sometimes ends.
app/
  lib/
    auth.go                    # Firebase nonsense
    auth_handlers.go           # Login, logout, the usual
    db.go                      # SQLite + WAL checkpoint goroutine
    middleware.go               # Session verification + user tracking + RequireSuperAdmin
  api/
    draw.go                    # REST handlers: save, load, share, rename, delete
    shared.go                  # Public drawing access (no auth, no problems)
    hub.go                     # WebSocket rooms, clients, broadcast (the noisy part)
    ws.go                      # WS upgrade + read/write pumps (the plumbing)
  canvas/
    page.go                    # Canvas handler
    page.templ                 # Excalidraw + dirty-bit + lazy socket JS
    shared.go                  # Shared canvas handler
    shared.templ               # Read-only view with WS for live updates
  dashboard/
    page.go                    # Dashboard + drawing creation
    page.templ                 # Grid of drawings you forgot existed
  profile/
    page.go                    # Profile handler
    page.templ                 # It's you. In text form.
  admin/
    page.go                    # All admin handlers (you can't see them)
    page.templ                 # Admin layout, sidebar, pages (still can't see them)
  components/
    navigation.templ           # The bar at the top
    logo.templ                 # It's a logo. We made it ourselves.
    drawing_card.templ         # Thumbnail + rename + delete
    footer.templ               # Legal boilerplate nobody reads
    empty_state.templ          # "You have no drawings. Sad." — this component
  layout.templ                 # The HTML shell. Every page wears it.
  canvas_layout.templ          # Minimal shell for canvas pages. No footer. No distractions.
  page.templ                   # Landing page. The first thing you see.
  page.go                      # Landing page handler. Redirects you if you're logged in.
  globals.css                  # All the CSS. Catppuccin. Brutalist. Beautiful.
  assets/
    excalidraw/
      entry.js                 # The glue that bundles Excalidraw
      package.json             # Dependency jail
    public/
      excalidraw.bundle.js     # 8 MB of someone else's code
      excalidraw.css           # Their styles
      logo.svg                 # Our logo. It's a circle and some lines.
      manifest.json            # PWA stuff
    assets.go                  # //go:embed so the binary isn't lonely
tools/
  generate_css/main.go         # Scans .templ files, extracts Tailwind classes, writes _entry.css
```

---

## Design System

**Catppuccin Brutalist.** Sharp corners. Hard shadows. If it doesn't have a 2px border, is it even real?

- **Fonts:** Bungee (loud), Space Mono (everything else)
- **Borders:** 2px. Minimum. 1px borders are for people who don't respect themselves.
- **Shadows:** Hard offset, zero blur. `shadow-[2px_2px_0px_0px_var(--accent)]` or death.
- **Border radius:** 0. Nothing is round. Not even the buttons. Especially not the buttons.
- **Buttons:** On click they translate and the shadow disappears. Like they're pressing into the screen. It feels satisfying. Try it.
- **Dark mode:** Follows system preference. Toggle persists to localStorage because we know you change it every 20 minutes depending on which part of the room the sun is hitting.

### Color Tokens

| Token | Light (Latte) | Dark (Mocha) | Jobs |
|---|---|---|---|
| `--bg` | `#eff1f5` | `#1e1e2e` | Background. The canvas upon which we paint. |
| `--fg` | `#4c4f69` | `#cdd6f4` | Text. Read it. |
| `--fg-muted` | `#8c8fa1` | `#6c7086` | Text that isn't important. Like this sentence. |
| `--bg-subtle` | `#e6e9ef` | `#181825` | Slightly different background. For cards. And the admin sidebar. |
| `--border` | `#4c4f69` | `#cdd6f4` | Lines that separate things. Like the line between you and productivity. |
| `--accent` | `#8839ef` | `#cba6f7` | Primary purple. Buttons, links, active nav items. |
| `--mauve` | `#8839ef` | `#cba6f7` | Secondary purple. For when one purple wasn't enough. |
| `--pink` | `#ea76cb` | `#f5c2e7` | Pink. Feature cards, hover states, the "revoke" button on the VIP page. |
| `--peach` | `#fe640b` | `#fab387` | Orange. Accent borders and the Minecraft-style splash text. |
| `--teal` | `#179299` | `#94e2d5` | Teal. Feature cards, "shared" badge, the third step in "how it works." |
| `--blue` | `#1e66f5` | `#89b4fa` | Blue. Grid lines, links, step 2. |
| `--lavender` | `#7287fd` | `#b4befe` | Lavender. Grid lines on the landing page and secondary accent. |

---

## Deployment

Ships as a single binary. Deployed via atomic symlink swap. Zero downtime, unless you break the symlink, in which case: downtime.

```bash
bash deploy.sh
```

### What Happens When You Run deploy.sh

1. **Local build:** `make css && make templ && CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build`
2. **Rsync:** Binary + Firebase credentials + server Makefile → timestamped folder on the server
3. **Atomic swap:** `ln -nfs current/` → new release. Users see zero interruption, unless they're in the middle of saving, in which case they lose 3 seconds of work maximum. We call that "acceptable."
4. **Symlink DB:** `canvas.db` lives in `shared/`. Deploys never touch it. Your drawings survive. We're not monsters.
5. **Restart:** `systemctl restart udin-canvas`. Server goes down for ~500ms. Browser retries. Nobody notices.
6. **Cleanup:** Old releases archived to `.tar.xz`. Last 5 kept. The rest? Gone. History. Reduced to atoms.

Binary is ~61 MB. Deploy takes ~50 seconds. That's 50 seconds of your life you'll never get back. Use them wisely.

### Server Setup

| Problem | How we fixed it |
|---|---|
| Reverse proxy | Caddy → Go app. Caddy handles TLS. We handle boxes. |
| Process supervision | systemd. `MemoryMax=128M`. If it goes over, it dies. That's not a bug, it's a feature. |
| Database persistence | Symlinked SQLite outside the release tree. |
| CDN | Cloudflare. Aggressive caching. We purge on CSS changes because Cloudflare once served a 3-day-old stylesheet and we didn't notice for 3 days. |
| Memory at rest | ~16 MB. |
| Memory under 50 WS users | ~96 MB. |
| Memory at death | ~128 MB. |

### Environment Variables

| Variable | Required | Default | What it does |
|---|---|---|---|
| `PORT` | No | `3000` | The port. Defaults to 3000. Change it if you want. Or don't. I'm not your manager. |
| `SQLITE_DB_PATH` | No | `./canvas.db` | Where the database lives. Don't move it while the server is running unless you want corrupted data. You've been warned. |

Firebase service account is auto-detected by scanning for `*-firebase-adminsdk-*.json` in the working directory. We're not making you set 50 environment variables. We're not that kind of project.

---

## The Suffering Log

Every stupid thing that broke, in something approaching chronological order.

1. **Firestore → SQLite.** We moved from a managed cloud database to a single file. Rewrote every query. The app got faster. The ops burden went to zero. Best decision we ever made. Took a weekend.

2. **Login/logout routing.** The logout form had `hx-boost="false"` which is apparently the HTMX equivalent of "this form is legally dead." The `HX-Redirect` header was being emitted into the void. Removed `hx-boost="false"`. Everything worked. Wasted 2 hours.

3. **Excalidraw canvas font.** `body { font-family: 'Space Mono' }` leaked into Excalidraw's text input. Every text node looked like a Linux terminal. Added a CSS reset for `.excalidraw .text-editor`. It mostly works now. Mostly.

4. **Tailwind v4 responsive classes silently disappeared.** `sm:hidden`, `md:flex`, `lg:grid-cols-3` — all of them, gone. No warning. No error. Just a broken mobile layout in production. Tailwind v4's `@source` directive doesn't scan `.templ` files. We built a Go tool that scans them and injects the classes into an `@source inline()` call. This is a workaround for something that shouldn't be broken in the first place.

5. **Cloudflare cached a 3-day-old CSS file.** `Cache-Control: no-cache` was a suggestion, not a rule. Switched to content-hashed URLs with `immutable`. Now when CSS changes, the URL changes, and Cloudflare has no choice but to fetch the new one. We still purge on deploy because we have trust issues.

6. **Auth bar required an OOB swap.** The desktop nav has `#auth-bar`. The mobile nav has `#auth-bar-mobile`. They both need to update when the user logs in. HTMX handles this with `hx-swap-oob="true"`. The handler has to return both elements in a single response. Works great now. Took entirely too long to figure out.

7. **Autosave race on tab close.** The debounce timer reset on every `onChange`. If you changed something and immediately closed the tab, the save never fired because the debounce timer was waiting. Maximum data loss: infinite. Replaced with dirty-bit + `setInterval` + `sendBeacon` on `beforeunload`. Maximum data loss: 3 seconds. Acceptable.

8. **WAL file grew to 4.2 GB.** SQLite's WAL auto-checkpoints at 1000 pages by default. If your server runs for weeks between restarts (like ours does), the WAL file accumulates until `df -h` says `Use% 100%`. Added a background goroutine that runs `PRAGMA wal_checkpoint(PASSIVE)` every 5 minutes. Fixed. Don't ask why this isn't the default.

9. **WebSocket `socket.readyState` is undefined in k6.** Of course it is. k6 v2.1.0's Socket object doesn't expose `readyState`. The guard `if (socket.readyState === 1)` silently blocked every SCENE_UPDATE send during the first load test. Zero updates sent. Load test looked amazing until we realized nothing was happening. Removed the guard. Problem solved. Trust issues: acquired.

10. **OOM at 128 MB with full SCENE_UPDATE.** Broadcasting 50 KB frames to 50 connections fills TCP buffers faster than a slow client can drain them. One slow client → buffer full → DROP → disconnect → reconnect → more goroutines → more memory → OOM → systemd: "lol bye." The cascade is beautiful in retrospect. Horrifying in production.

11. **SCENE_DELTA phantom diffs because `_sceneElements` wasn't updated.** `handleRemoteScene` called `api.updateScene()` but didn't update `_sceneElements`. Next `computeDelta` computed against stale elements. Sent the entire scene as a "delta." Delta size: 50 KB. Congratulations, you just invented full SCENE_UPDATE with extra steps.

12. **SCENE_DELTA was too fragile to live.** The delta approach survived the load test. It did not survive real users. Phantom diffs. Stale caches. Race conditions between SCENE_INIT and local state. We deleted it. Full SCENE_UPDATE is simpler and works with 2-3 users. The OOM problem at 50 broadcasters doesn't happen with the lazy socket because most users don't have a socket open. Problem avoided, not solved. Still counts.

13. **There will be more bugs.** We know this. You know this. The only question is which one you'll find first.

---

## Getting Started

Prerequisites: Go 1.22+, Node.js (for esbuild), `gcc` (for CGO/SQLite).

```bash
make setup   # Installs templ, air, standalone Tailwind, npm install
make dev     # CSS watch + Air live-reload on :3000
```

Or if you're impatient and the Excalidraw bundle already exists:

```bash
go run .
```

Open `http://localhost:3000`. Sign in with Google. Make a drawing. It saves automatically. If something breaks, check The Suffering Log above. Your problem is probably there.

> **CGO note:** SQLite requires `CGO_ENABLED=1`. macOS: works out of the box. Linux: install `gcc`. Windows: good luck. The deploy script cross-compiles from macOS/Linux to the Linux server. We don't test on Windows. If you get it working on Windows, open a PR. We'll merge it. Eventually.

### Make Targets

| Target | What it does |
|---|---|
| `make setup` | Downloads the entire internet (templ, air, Tailwind, npm packages). Go make coffee. |
| `make dev` | Compiles CSS, starts watcher, starts Air. Saves you from manually restarting the server. |
| `make templ` | Regenerates Go code from `.templ` files. Run this if you edit a template. |
| `make css` | Scans `.templ` for classes, compiles Tailwind. We have a custom tool for this because Tailwind v4. |
| `make generate-css` | Just the scanning part. For when you need to debug missing classes. Again. |
| `make bundle` | esbuilds Excalidraw. Takes 30 seconds. Makes the binary 8 MB fatter. |
| `make build` | Everything above + `go build`. The full pipeline. The complete experience. |

---

## Why Not Use Excalidraw's Built-In Save?

Excalidraw has a "save to disk" button. It saves a `.excalidraw` file to your Downloads folder. If you use one computer forever and never want to open your diagrams anywhere else, that's fine.

If you want to draw on your laptop, open it on your desktop, and share it with someone without emailing a file, you need a server. That's us.

SQLite is less work than any managed database. No connection strings. No "provisioning." No "your free tier has expired." It's a file. Back it up with `cp`. Restore it with `cp`. Deploy it with `rsync`. It's a file.

---

## The Accidental React

We started this project specifically to avoid React. That was the goal. "HTMX can handle everything." And it did — for auth, navigation, the dashboard, profile pages, everything a normal website needs.

Then we got to the canvas.

Excalidraw is a React component. You can't HTMX your way into an infinite canvas. You can't `hx-trigger="load"` your way around mounting a React root. You have to write React. There's no shortcut.

The inline `<script>` block in `page.templ` started at 10 lines. It's now 200 lines managing WebSocket state, dirty-bit autosave, collaborator detection, and scene reconciliation. We tried to be clever. We built a delta protocol. We debugged phantom diffs at 2 AM. We used words like "reconciliation" and "version vector" in casual conversation. We were insufferable.

We deleted all of it. The current code sends a full SCENE_UPDATE on every change. It's dumb. It works. The lazy socket ensures the dumb part only affects the 2-3 people actively collaborating. Everyone else stays on HTTP and never knows the difference.

**We started not wanting to do React, we ended up doing React, in its intended way.** The React root renders Excalidraw. The inline script handles the bridge logic. HTMX handles everything else. Each tool does what it's good at. It just took us 2,000 lines of deleted delta code to figure that out.

---

## License

**WTFPL — Do What the Fuck You Want To Public License.**

This is free software. Go absolutely berserk.

Copy it. Fork it. Sell it. Put your name on it. Deploy it to a $5 VPS and charge people $50/month for access. Set your server on fire with it — I don't care, I'm not your sysadmin. The author is not responsible for anything that happens as a result of using this software, including but not limited to: database corruption, emotional damage from debugging WebSocket cascades, your boss asking why your architecture diagram looks like a plate of spaghetti, the realization that you cloned a GitHub repo at 11 PM on a Tuesday instead of sleeping, or the slow creeping dread that comes from maintaining a project long after the initial enthusiasm has faded.

It's free. You get what you pay for.

*Excalidraw is separately licensed under MIT — see NOTICE.*
