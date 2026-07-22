package app

import (
	"net/http"

	"gotth/app/middleware"
)

func PageHandler(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserUID(r.Context())
	if uid != "" {
		http.Redirect(w, r, "/drawings", http.StatusFound)
		return
	}
	LandingPage().Render(r.Context(), w)
}
