package dashboard

import (
	"log"
	"net/http"
	"sort"
	"time"
	"gotth/app/auth"

	"google.golang.org/api/iterator"
)

type Drawing struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Thumbnail string
}

func PageHandler(w http.ResponseWriter, r *http.Request) {
	uid := auth.GetUserUID(r.Context())
	if uid == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	iter := auth.Firestore.Collection("drawings").
		Where("ownerId", "==", uid).
		Documents(r.Context())

	var drawings []Drawing
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Firestore query: %v", err)
			break
		}
		d := Drawing{ID: doc.Ref.ID}
		doc.DataTo(&d)
		drawings = append(drawings, d)
	}

	sort.Slice(drawings, func(i, j int) bool {
		return drawings[i].UpdatedAt.After(drawings[j].UpdatedAt)
	})

	DashboardPage(drawings).Render(r.Context(), w)
}

func NewHandler(w http.ResponseWriter, r *http.Request) {
	uid := auth.GetUserUID(r.Context())
	if uid == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	ref := auth.Firestore.Collection("drawings").NewDoc()
	now := time.Now()

	_, err := ref.Set(r.Context(), map[string]interface{}{
		"ownerId":   uid,
		"title":     "Untitled",
		"createdAt": now,
		"updatedAt": now,
		"sceneData": `{"elements":[],"appState":{}}`,
	})
	if err != nil {
		log.Printf("create drawing: %v", err)
		http.Error(w, "failed to create drawing", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/draw/"+ref.ID, http.StatusFound)
}
