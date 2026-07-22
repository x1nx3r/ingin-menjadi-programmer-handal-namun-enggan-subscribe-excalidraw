package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"time"

	"gotth/app/lib"
	"gotth/app/middleware"
)

type Drawing struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Thumbnail string
}

func PageHandler(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserUID(r.Context())
	if uid == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	rows, err := lib.DB.QueryContext(r.Context(),
		`SELECT d.id, d.title, d.created_at, d.updated_at, COALESCE(t.data, '')
		 FROM drawings d
		 LEFT JOIN drawing_thumbnails t ON d.id = t.drawing_id
		 WHERE d.owner_id = ?
		 ORDER BY d.updated_at DESC`, uid)
	if err != nil {
		log.Printf("query drawings: %v", err)
		http.Error(w, "failed to load drawings", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var drawings []Drawing
	for rows.Next() {
		var d Drawing
		if err := rows.Scan(&d.ID, &d.Title, &d.CreatedAt, &d.UpdatedAt, &d.Thumbnail); err != nil {
			log.Printf("scan drawing: %v", err)
			continue
		}
		drawings = append(drawings, d)
	}

	DashboardPage(drawings).Render(r.Context(), w)
}

func NewHandler(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserUID(r.Context())
	if uid == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	var count int
	err := lib.DB.QueryRowContext(r.Context(), "SELECT COUNT(id) FROM drawings WHERE owner_id = ?", uid).Scan(&count)
	if err != nil {
		log.Printf("count drawings: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if count >= 30 {
		http.Redirect(w, r, "/drawings", http.StatusFound)
		return
	}

	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)

	now := time.Now()
	_, err = lib.DB.ExecContext(r.Context(),
		"INSERT INTO drawings (id, owner_id, title, content, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, uid, "Untitled", `{"elements":[],"appState":{}}`, now, now)
	if err != nil {
		log.Printf("create drawing: %v", err)
		http.Error(w, "failed to create drawing", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/draw/"+id, http.StatusFound)
}
