package profile

import (
	"log"
	"net/http"
	"sort"
	"time"
	"gotth/app/auth"
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
	uid := auth.GetUserUID(r.Context())
	if uid == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	user, err := auth.FirebaseAuth.GetUser(r.Context(), uid)
	if err != nil {
		log.Printf("get user: %v", err)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	iter := auth.Firestore.Collection("drawings").Where("ownerId", "==", uid).Documents(r.Context())
	docs, err := iter.GetAll()
	if err != nil {
		log.Printf("list drawings: %v", err)
		http.Error(w, "failed to load drawings", http.StatusInternalServerError)
		return
	}

	items := make([]DrawingItem, 0, len(docs))
	for _, d := range docs {
		data := d.Data()
		t, _ := data["title"].(string)
		ua, _ := data["updatedAt"].(time.Time)
		th, _ := data["thumbnail"].(string)
		items = append(items, DrawingItem{ID: d.Ref.ID, Title: t, UpdatedAt: ua, Thumbnail: th})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})

	name := user.DisplayName
	if name == "" {
		name = user.Email
	}

	Page(name, user.PhotoURL, items).Render(r.Context(), w)
}
