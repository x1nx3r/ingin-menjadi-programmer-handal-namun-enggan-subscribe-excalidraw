# File Storage for Excalidraw Images

## Overview

Excalidraw allows users to paste/drop images onto the canvas. These images
are stored as separate blob files rather than inline in the scene JSON.
This keeps scene data small and allows independent caching.

## Data Flow

### Upload (paste/drop → disk)

```
User pastes image
       │
       ▼
Excalidraw stores as base64 dataURL internally
  (this is unavoidable — Excalidraw's native format)
       │
       ▼
onChange(elements, appState, files) detects new fileId
       │
       ├── SVG → fetch as blob, upload raw bytes
       │         Content-Type: image/svg+xml
       │
       └── non-SVG → Image → canvas → toBlob('image/webp', 0.85)
                      upload raw WebP bytes
                      Content-Type: image/webp
       │
       ▼
POST /api/draw/{id}/file?fileId=<uuid>
  Content-Type: image/webp (or image/svg+xml)
  Body: <raw bytes>

Server:
  ├─  io.ReadAll(body)  ← no JSON parse, no base64 decode
  ├─  check quota in transaction
  ├─  os.WriteFile(files/{drawingId}/{fileId}, blob)
  ├─  INSERT drawing_files row
  └─  UPDATE storage_used_bytes
```

### Scene Save (interval flush)

```
flushIfDirty() fires every 3s
       │
       ▼
POST /api/draw/{id}/save
  Body: {"elements":[...], "appState":{...}}
       │
       └─ elements only contain fileId references
          no image data in the payload → stays small
```

### Scene Load (page visit)

```
GET /api/draw/{id}/data
       │
       ▼
Response: {"elements":[...], "appState":{...}, "files":[{"id":"...","mimeType":"image/webp"}]}
  (files is a metadata array — no base64 blobs)
       │
       ▼
Client: api.updateScene({elements, appState})  ← renders immediately
       │
       ▼
Client: Promise.all(data.files.map(f =>
  fetch('/api/file/{drawingId}/' + f.id)  ← parallel fetches
    → r.blob() → FileReader → dataURL
))
       │
       ▼
Client: api.addFiles(fileMap)  ← images pop in when ready
```

### File Serving

```
GET /api/file/{drawingId}/{fileId}
  Access: owner of drawing OR drawing has share_slug (public)
  Response: raw bytes
  Cache-Control: public, max-age=31536000, immutable
  (browser caches forever — never re-fetches on subsequent visits)
```

## Wire Efficiency

| What | Wire format | Notes |
|------|-------------|-------|
| Upload | Raw WebP bytes | No base64 envelope, no JSON wrapper |
| Scene save | JSON with fileId refs | No image data at all |
| Scene load | JSON with file metadata | Tiny — just id + mimeType array |
| File fetch | Raw WebP bytes | Browser caches with immutable max-age |

## Storage

- **Path**: `files/{drawingId}/{fileId}` (derived from `SQLITE_DB_PATH`)
- **Format**: Raw WebP bytes (or raw SVG text)
- **No server compression**: Caddy handles HTTP-level gzip; storing compressed
  would cause double-compression bugs when serving
- **Cleanup**: Full `files/{drawingId}/` directory removed on drawing delete

## Quota

- **Default**: 30 MB per user (`storage_max_bytes = 31457280`)
- **Unlimited**: `storage_max_bytes = -1`
- **Counter**: `storage_used_bytes` — incremented/decremented within the same
  transaction as file insert/delete (`MaxOpenConns(1)` ensures safety)
- **Admin**: Grant/revoke unlimited per-user from the admin panel

## Key Design Decisions

1. **Client-side WebP conversion**: Avoids server CPU tax. Users pay a one-time
   canvas render per image upload (imperceptible). WebP is 50-80% smaller than
   PNG for the same visual quality.

2. **Separate serving endpoint** instead of inline base64: Scene JSON stays
   small regardless of image count/size. Files can be cached independently
   by the browser with immutable max-age.

3. **Content-Type as mime type**: The `Content-Type` header on the upload POST
   becomes the stored mime type. No separate field needed. SVG vs WebP is
   distinguished purely by this header.

4. **No storage-layer compression**: Avoids double-gzip when Caddy serves.
   The wire still benefits from Caddy's HTTP-level gzip on text content
   (JSON responses), while binary WebP files pass through uncompressed
   (WebP is already compressed internally).

## Database Schema

```sql
CREATE TABLE drawing_files (
    id         TEXT NOT NULL,
    drawing_id TEXT NOT NULL REFERENCES drawings(id) ON DELETE CASCADE,
    owner_id   TEXT NOT NULL,
    mime_type  TEXT NOT NULL,
    file_size  INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (drawing_id, id)
);

ALTER TABLE users ADD COLUMN storage_max_bytes  INTEGER NOT NULL DEFAULT 31457280;
ALTER TABLE users ADD COLUMN storage_used_bytes INTEGER NOT NULL DEFAULT 0;
```
