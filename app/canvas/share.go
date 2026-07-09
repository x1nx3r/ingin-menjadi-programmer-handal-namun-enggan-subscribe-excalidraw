package canvas

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"gotth/app/auth"
)

type shareEntry struct {
	Slug      string    `firestore:"slug"`
	DrawingID string    `firestore:"drawingId"`
	CreatedAt time.Time `firestore:"createdAt"`
}

func generateSlug() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ShareHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := auth.GetUserUID(r.Context())

	doc, err := auth.Firestore.Collection("drawings").Doc(id).Get(r.Context())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := doc.Data()
	ownerId, _ := data["ownerId"].(string)
	if ownerId != uid {
		http.NotFound(w, r)
		return
	}

	existingSlug, _ := data["shareSlug"].(string)
	if existingSlug != "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"slug": existingSlug})
		return
	}

	slug, err := generateSlug()
	if err != nil {
		http.Error(w, "slug generation failed", http.StatusInternalServerError)
		return
	}

	_, err = auth.Firestore.Collection("shares").Doc(slug).Set(r.Context(), shareEntry{
		Slug:      slug,
		DrawingID: id,
		CreatedAt: time.Now(),
	})
	if err != nil {
		http.Error(w, "share creation failed", http.StatusInternalServerError)
		return
	}

	_, err = auth.Firestore.Collection("drawings").Doc(id).Set(r.Context(), map[string]interface{}{
		"shareSlug": slug,
		"updatedAt": time.Now(),
	}, firestore.MergeAll)
	if err != nil {
		http.Error(w, "share update failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"slug": slug})
}

func SharedDataHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	shareSnap, err := auth.Firestore.Collection("shares").Doc(slug).Get(r.Context())
	if err != nil {
		http.NotFound(w, r)
		return
	}

	share := shareSnap.Data()
	drawingID, _ := share["drawingId"].(string)
	if drawingID == "" {
		http.NotFound(w, r)
		return
	}

	drawing, err := auth.Firestore.Collection("drawings").Doc(drawingID).Get(r.Context())
	if err != nil {
		http.NotFound(w, r)
		return
	}

	sanitizedSceneData(w, drawing.Data()["sceneData"])
}

func sanitizedSceneData(w http.ResponseWriter, raw any) {
	sceneStr, _ := raw.(string)
	sanitized := sanitizeSceneJSON(sceneStr)
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
