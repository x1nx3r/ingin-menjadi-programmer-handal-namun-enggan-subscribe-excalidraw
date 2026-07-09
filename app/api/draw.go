package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"gotth/app/lib"
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
	uid := lib.GetUserUID(r.Context())
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

	sanitizedSceneData(w, content)
}

func SaveHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var sd sceneData
	if err := json.Unmarshal(body, &sd); err != nil {
		http.Error(w, "invalid scene data", http.StatusBadRequest)
		return
	}

	_, err = lib.DB.ExecContext(r.Context(),
		"UPDATE drawings SET content = ?, updated_at = ? WHERE id = ?",
		string(body), time.Now(), id)
	if err != nil {
		log.Printf("save drawing %s: %v", id, err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func ShareHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := lib.GetUserUID(r.Context())

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

	slug, err := generateSlug()
	if err != nil {
		http.Error(w, "slug generation failed", http.StatusInternalServerError)
		return
	}

	_, err = lib.DB.ExecContext(r.Context(),
		"UPDATE drawings SET share_slug = ?, updated_at = ? WHERE id = ?",
		slug, time.Now(), id)
	if err != nil {
		http.Error(w, "share creation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"slug": slug})
}

func RenameHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	_, err = lib.DB.ExecContext(r.Context(),
		"UPDATE drawings SET thumbnail = ?, updated_at = ? WHERE id = ?",
		string(body), time.Now(), id)
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !checkOwnership(r, id) {
		http.NotFound(w, r)
		return
	}

	_, err := lib.DB.ExecContext(r.Context(), "DELETE FROM drawings WHERE id = ?", id)
	if err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func sanitizedSceneData(w http.ResponseWriter, raw string) {
	sanitized := sanitizeSceneJSON(raw)
	w.Header().Set("Content-Type", "application/json")
	w.Write(sanitized)
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
