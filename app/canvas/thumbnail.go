package canvas

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
	"cloud.google.com/go/firestore"
	"gotth/app/auth"
)

func ThumbnailHandler(w http.ResponseWriter, r *http.Request) {
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	_, err = auth.Firestore.Collection("drawings").Doc(id).Set(r.Context(), map[string]interface{}{
		"thumbnail":   string(body),
		"updatedAt":   time.Now(),
	}, firestore.MergeAll)
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
