package api

import (
	"net/http"

	"gotth/app/lib"
)

func SharedDataHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var content string
	err := lib.DB.QueryRowContext(r.Context(), "SELECT content FROM drawings WHERE share_slug = ?", slug).Scan(&content)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	sanitizedSceneData(w, content)
}
