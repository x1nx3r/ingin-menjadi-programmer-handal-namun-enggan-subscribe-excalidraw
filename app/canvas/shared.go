package canvas

import (
	"net/http"

	"gotth/app/lib"
)

func SharedPageHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var title string
	err := lib.DB.QueryRowContext(r.Context(), "SELECT title FROM drawings WHERE share_slug = ?", slug).Scan(&title)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	SharedPage(title, slug).Render(r.Context(), w)
}
