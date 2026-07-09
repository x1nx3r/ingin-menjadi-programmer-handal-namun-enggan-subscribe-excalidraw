package canvas

import (
	"net/http"
	"gotth/app/auth"
)

func SharedPageHandler(w http.ResponseWriter, r *http.Request) {
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

	title := "Untitled"
	if t, ok := drawing.Data()["title"].(string); ok && t != "" {
		title = t
	}

	SharedPage(title, slug).Render(r.Context(), w)
}
