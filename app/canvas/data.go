package canvas

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"gotth/app/auth"
)

type sceneData struct {
	Elements json.RawMessage `json:"elements"`
	AppState json.RawMessage `json:"appState"`
}

func loadDrawing(r *http.Request, id string) (map[string]interface{}, bool) {
	uid := auth.GetUserUID(r.Context())
	if uid == "" {
		return nil, false
	}

	doc, err := auth.Firestore.Collection("drawings").Doc(id).Get(r.Context())
	if err != nil {
		return nil, false
	}

	data := doc.Data()
	ownerId, _ := data["ownerId"].(string)
	if ownerId != uid {
		return nil, false
	}

	return data, true
}

func DataHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data, ok := loadDrawing(r, id)
	if !ok {
		http.NotFound(w, r)
		return
	}

	sanitizedSceneData(w, data["sceneData"])
}

func SaveHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data, ok := loadDrawing(r, id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = data

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

	_, err = auth.Firestore.Collection("drawings").Doc(id).Set(r.Context(), map[string]interface{}{
		"sceneData": string(body),
		"updatedAt": time.Now(),
	}, firestore.MergeAll)
	if err != nil {
		log.Printf("save drawing %s: %v", id, err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
