package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gotth/app/lib"
	"gotth/app/middleware"

	"github.com/mattn/go-sqlite3"
)

type sceneData struct {
	Elements json.RawMessage `json:"elements"`
	AppState json.RawMessage `json:"appState"`
}

func generateSlug() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func checkOwnership(r *http.Request, id string) bool {
	uid := middleware.GetUserUID(r.Context())
	if uid == "" {
		return false
	}
	var ownerId string
	err := lib.DB.QueryRowContext(r.Context(), "SELECT owner_id FROM drawings WHERE id = ?", id).Scan(&ownerId)
	return err == nil && ownerId == uid
}

func DataHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	var content string
	err := lib.DB.QueryRowContext(r.Context(), "SELECT content FROM drawings WHERE id = ?", id).Scan(&content)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	writeSceneWithFiles(w, id, content)
}

func SaveHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024) // 5 MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	var sd sceneData
	if err := json.Unmarshal(body, &sd); err != nil {
		http.Error(w, "invalid scene data", http.StatusBadRequest)
		return
	}

	res, err := lib.DB.ExecContext(r.Context(),
		"UPDATE drawings SET content = ?, updated_at = ? WHERE id = ?",
		string(body), time.Now(), id)
	if err != nil {
		log.Printf("save drawing %s: %v", id, err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		http.NotFound(w, r)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func ShareHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := middleware.GetUserUID(r.Context())

	var ownerId string
	var existingSlug sql.NullString
	err := lib.DB.QueryRowContext(r.Context(), "SELECT owner_id, share_slug FROM drawings WHERE id = ?", id).Scan(&ownerId, &existingSlug)
	if err != nil || ownerId != uid {
		http.NotFound(w, r)
		return
	}

	if existingSlug.Valid && existingSlug.String != "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"slug": existingSlug.String})
		return
	}

	// Retry on unique-constraint collisions (16 hex chars is usually unique,
	// but a race or a very large database can collide).
	const maxRetries = 5
	var slug string
	for i := 0; i < maxRetries; i++ {
		slug, err = generateSlug()
		if err != nil {
			http.Error(w, "slug generation failed", http.StatusInternalServerError)
			return
		}

		_, err = lib.DB.ExecContext(r.Context(),
			"UPDATE drawings SET share_slug = ?, updated_at = ? WHERE id = ?",
			slug, time.Now(), id)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"slug": slug})
			return
		}
		var sqliteErr sqlite3.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrConstraint {
			log.Printf("share slug collision for drawing %s, retrying", id)
			continue
		}
		http.Error(w, "share creation failed", http.StatusInternalServerError)
		return
	}

	http.Error(w, "share creation failed: too many collisions", http.StatusInternalServerError)
}

func RenameHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" {
		http.Error(w, "missing or invalid title", http.StatusBadRequest)
		return
	}

	_, err := lib.DB.ExecContext(r.Context(),
		"UPDATE drawings SET title = ?, updated_at = ? WHERE id = ?",
		body.Title, time.Now(), id)
	if err != nil {
		http.Error(w, "rename failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func ThumbnailHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 256*1024) // 256 KB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "thumbnail too large", http.StatusRequestEntityTooLarge)
		return
	}

	_, err = lib.DB.ExecContext(r.Context(),
		"INSERT OR REPLACE INTO drawing_thumbnails (drawing_id, data, updated_at) VALUES (?, ?, datetime('now'))",
		id, string(body))
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func SaveFileHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	fileID := r.URL.Query().Get("fileId")
	if fileID == "" {
		http.Error(w, "missing fileId", http.StatusBadRequest)
		return
	}

	mimeType := r.Header.Get("Content-Type")
	if mimeType == "" {
		http.Error(w, "missing Content-Type", http.StatusBadRequest)
		return
	}
	if !lib.ValidateFileMIME(mimeType) {
		http.Error(w, "unsupported file type", http.StatusUnsupportedMediaType)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024) // 10 MB limit
	blob, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	fileSize := int64(len(blob))

	uid := middleware.GetUserUID(r.Context())
	storageID := lib.GenerateStorageID(id, fileID)
	filePath := lib.StorageFilePath(id, storageID)
	if filePath == "" {
		http.Error(w, "invalid file path", http.StatusInternalServerError)
		return
	}

	// Check quota (unless unlimited).
	tx, err := lib.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var maxBytes, usedBytes int64
	err = tx.QueryRowContext(r.Context(),
		`SELECT storage_max_bytes, storage_used_bytes FROM users WHERE uid = ?`, uid,
	).Scan(&maxBytes, &usedBytes)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	var oldSize int64
	_ = tx.QueryRowContext(r.Context(),
		`SELECT COALESCE(file_size, 0) FROM drawing_files WHERE drawing_id = ? AND id = ?`,
		id, fileID,
	).Scan(&oldSize)

	if maxBytes >= 0 && usedBytes+fileSize-oldSize > maxBytes {
		http.Error(w, "quota exceeded", http.StatusInsufficientStorage)
		return
	}

	// Write to disk using the server-side storage_id, never the client fileId.
	dir := filepath.Join(lib.StorageRoot(), id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("mkdir files %s: %v", id, err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filePath, blob, 0644); err != nil {
		log.Printf("write file %s/%s: %v", id, storageID, err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	// Insert DB row and update quota in the same transaction.
	_, err = tx.ExecContext(r.Context(),
		`INSERT OR REPLACE INTO drawing_files (id, drawing_id, owner_id, storage_id, mime_type, file_size) VALUES (?, ?, ?, ?, ?, ?)`,
		fileID, id, uid, storageID, lib.NormalizeFileMIME(mimeType), fileSize,
	)
	if err != nil {
		log.Printf("insert drawing_file %s/%s: %v", id, fileID, err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	_, err = tx.ExecContext(r.Context(),
		`UPDATE users SET storage_used_bytes = storage_used_bytes - ? + ? WHERE uid = ?`,
		oldSize, fileSize, uid,
	)
	if err != nil {
		log.Printf("update quota %s: %v", uid, err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("commit file tx %s/%s: %v", id, fileID, err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func DeleteFileHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	fileID := r.URL.Query().Get("fileId")
	if fileID == "" {
		http.Error(w, "missing fileId", http.StatusBadRequest)
		return
	}

	uid := middleware.GetUserUID(r.Context())

	tx, err := lib.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var fileSize int64
	var storageID string
	err = tx.QueryRowContext(r.Context(),
		`DELETE FROM drawing_files WHERE drawing_id = ? AND id = ? AND owner_id = ? RETURNING file_size, storage_id`,
		id, fileID, uid,
	).Scan(&fileSize, &storageID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Remove from disk (best-effort) using the server-side storage_id.
	if filePath := lib.StorageFilePath(id, storageID); filePath != "" {
		os.Remove(filePath)
	}

	_, err = tx.ExecContext(r.Context(),
		`UPDATE users SET storage_used_bytes = MAX(0, storage_used_bytes - ?) WHERE uid = ?`, fileSize, uid,
	)
	if err != nil {
		log.Printf("update quota on delete %s: %v", uid, err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("commit delete file tx %s/%s: %v", id, fileID, err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func PublicEditHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	// Gate: only VIP-whitelisted users can toggle public edit.
	email := middleware.GetUserEmail(r.Context())
	var vipCount int
	_ = lib.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM feature_whitelist WHERE email = ?`, email,
	).Scan(&vipCount)
	if vipCount == 0 {
		http.Error(w, "feature not available", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 128)
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	val := 0
	if body.Enabled {
		val = 1
	}
	if _, err := lib.DB.ExecContext(r.Context(),
		"UPDATE drawings SET allow_public_edits = ?, updated_at = ? WHERE id = ?",
		val, time.Now(), id); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	uid := middleware.GetUserUID(r.Context())

	tx, err := lib.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Collect file sizes for quota adjustment before cascade deletes them.
	var totalSize int64
	_ = tx.QueryRowContext(r.Context(),
		`SELECT COALESCE(SUM(file_size), 0) FROM drawing_files WHERE drawing_id = ? AND owner_id = ?`,
		id, uid,
	).Scan(&totalSize)

	// Adjust quota before the cascade delete — if this fails we can still retry.
	if totalSize > 0 {
		_, err = tx.ExecContext(r.Context(),
			`UPDATE users SET storage_used_bytes = MAX(0, storage_used_bytes - ?) WHERE uid = ?`,
			totalSize, uid,
		)
		if err != nil {
			http.Error(w, "quota update failed", http.StatusInternalServerError)
			return
		}
	}

	// Remove files from disk (best-effort — we'll still commit the DB change).
	os.RemoveAll(filepath.Join(lib.StorageRoot(), id))

	_, err = tx.ExecContext(r.Context(), "DELETE FROM drawings WHERE id = ?", id)
	if err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "commit failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func sanitizeSceneJSON(raw string) []byte {
	if raw == "" {
		raw = `{"elements":[],"appState":{}}`
	}

	var parsed struct {
		Elements json.RawMessage `json:"elements"`
		AppState map[string]any  `json:"appState"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return []byte(`{"elements":[],"appState":{}}`)
	}

	if parsed.AppState != nil {
		parsed.AppState["collaborators"] = []any{}
	}

	sanitized, _ := json.Marshal(parsed)
	return sanitized
}

type fileEntry struct {
	ID       string `json:"id"`
	MimeType string `json:"mimeType"`
}

func writeSceneWithFiles(w http.ResponseWriter, drawingID, content string) {
	sanitized := sanitizeSceneJSON(content)

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(sanitized, &parsed); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(sanitized)
		return
	}

	files := listFiles(drawingID)
	fileJSON, _ := json.Marshal(files)
	parsed["files"] = fileJSON

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(parsed)
}

func listFiles(drawingID string) []fileEntry {
	rows, err := lib.DB.Query(
		`SELECT id, mime_type FROM drawing_files WHERE drawing_id = ?`, drawingID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var files []fileEntry
	for rows.Next() {
		var f fileEntry
		if err := rows.Scan(&f.ID, &f.MimeType); err != nil {
			continue
		}
		files = append(files, f)
	}
	return files
}

func ServeFileHandler(w http.ResponseWriter, r *http.Request) {
	drawingID := r.PathValue("drawingId")
	fileID := r.PathValue("fileId")

	// Verify the file exists and get its server-side storage_id and mime type.
	var mimeType, storageID string
	err := lib.DB.QueryRowContext(r.Context(),
		`SELECT mime_type, storage_id FROM drawing_files WHERE drawing_id = ? AND id = ?`, drawingID, fileID,
	).Scan(&mimeType, &storageID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Access control: owner of the drawing OR a publicly shared drawing.
	uid := middleware.GetUserUID(r.Context())
	var ownerID string
	var shareSlug sql.NullString
	_ = lib.DB.QueryRowContext(r.Context(),
		`SELECT owner_id, share_slug FROM drawings WHERE id = ?`, drawingID,
	).Scan(&ownerID, &shareSlug)

	hasAccess := uid != "" && uid == ownerID
	if !hasAccess && shareSlug.Valid && shareSlug.String != "" {
		hasAccess = true
	}
	if !hasAccess {
		http.NotFound(w, r)
		return
	}

	filePath := lib.StorageFilePath(drawingID, storageID)
	if filePath == "" {
		http.NotFound(w, r)
		return
	}

	blob, err := os.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Serve with a safe MIME type. If the stored type was ever not in the
	// allowlist, force it to application/octet-stream so it cannot execute.
	w.Header().Set("Content-Type", lib.NormalizeFileMIME(mimeType))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !lib.ValidateFileMIME(mimeType) {
		w.Header().Set("Content-Disposition", "attachment")
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(blob)
}
