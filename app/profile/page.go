package profile

import (
	"log"
	"net/http"
	"time"

	"gotth/app/lib"
)

type DrawingItem struct {
	ID        string
	Title     string
	UpdatedAt time.Time
	Thumbnail string
}

func drawingLabel(n int) string {
	if n == 1 {
		return "drawing"
	}
	return "drawings"
}

func PageHandler(w http.ResponseWriter, r *http.Request) {
	uid := lib.GetUserUID(r.Context())
	if uid == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	user, err := lib.FirebaseAuth.GetUser(r.Context(), uid)
	if err != nil {
		log.Printf("get user: %v", err)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	rows, err := lib.DB.QueryContext(r.Context(),
		"SELECT id, title, updated_at, COALESCE(thumbnail, '') FROM drawings WHERE owner_id = ? ORDER BY updated_at DESC", uid)
	if err != nil {
		log.Printf("list drawings: %v", err)
		http.Error(w, "failed to load drawings", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var items []DrawingItem
	for rows.Next() {
		var item DrawingItem
		if err := rows.Scan(&item.ID, &item.Title, &item.UpdatedAt, &item.Thumbnail); err != nil {
			log.Printf("scan drawing: %v", err)
			continue
		}
		items = append(items, item)
	}

	name := user.DisplayName
	if name == "" {
		name = user.Email
	}

	ProfilePage(name, user.PhotoURL, items).Render(r.Context(), w)
}
