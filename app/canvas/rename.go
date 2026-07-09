package canvas

import (
	"encoding/json"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"gotth/app/auth"
)

func RenameHandler(w http.ResponseWriter, r *http.Request) {
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

	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" {
		http.Error(w, "missing or invalid title", http.StatusBadRequest)
		return
	}

	_, err = auth.Firestore.Collection("drawings").Doc(id).Set(r.Context(), map[string]interface{}{
		"title":     body.Title,
		"updatedAt": time.Now(),
	}, firestore.MergeAll)
	if err != nil {
		http.Error(w, "rename failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
