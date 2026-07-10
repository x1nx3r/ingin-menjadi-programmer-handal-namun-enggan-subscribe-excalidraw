package canvas

import (
	"log"
	"net/http"

	"gotth/app/lib"
)

func PageHandler(w http.ResponseWriter, r *http.Request) {
	uid := lib.GetUserUID(r.Context())
	if uid == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	id := r.PathValue("id")

	var ownerId, title string
	err := lib.DB.QueryRowContext(r.Context(), "SELECT owner_id, title FROM drawings WHERE id = ?", id).Scan(&ownerId, &title)
	if err != nil {
		log.Printf("load drawing %s: %v", id, err)
		http.NotFound(w, r)
		return
	}

	if ownerId != uid {
		http.NotFound(w, r)
		return
	}

	// Check if the current user is on the VIP whitelist.
	var isVIP bool
	if email := lib.GetUserEmail(r.Context()); email != "" {
		var count int
		_ = lib.DB.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM feature_whitelist WHERE email = ?`, email,
		).Scan(&count)
		isVIP = count > 0
	}

	CanvasPage(title, id, isVIP).Render(r.Context(), w)
}

