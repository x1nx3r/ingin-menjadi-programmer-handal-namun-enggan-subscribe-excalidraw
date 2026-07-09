package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"gotth/app"
	"gotth/app/assets"
	"gotth/app/auth"
	"gotth/app/canvas"
	"gotth/app/dashboard"
	"gotth/app/docs"
	"gotth/app/profile"
	_ "github.com/a-h/templ"
)

func main() {
	auth.Init()

	mux := http.NewServeMux()

	// Globals CSS (embedded binary)
	mux.Handle("GET /globals.css", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Write(assets.CSS)
	}))

	// Static assets (embedded under public/)
	publicFS, err := fs.Sub(assets.Public, "public")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(publicFS))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))

	// Auth
	mux.HandleFunc("POST /auth/login", auth.LoginHandler)
	mux.HandleFunc("POST /auth/logout", auth.LogoutHandler)
	mux.HandleFunc("GET /auth/user", auth.UserHandler)

	// Landing (public) / Dashboard (auth required)
	mux.HandleFunc("GET /{$}", app.PageHandler)
	mux.Handle("GET /drawings", auth.RequireAuth(dashboard.PageHandler))
	mux.Handle("POST /draw/new", auth.RequireAuth(dashboard.NewHandler))

	// Canvas editor
	mux.Handle("GET /draw/{id}", auth.RequireAuth(canvas.PageHandler))

	// Canvas data API
	mux.Handle("GET /api/draw/{id}/data", auth.RequireAuth(canvas.DataHandler))
	mux.Handle("POST /api/draw/{id}/save", auth.RequireAuth(canvas.SaveHandler))
	mux.Handle("POST /api/draw/{id}/share", auth.RequireAuth(canvas.ShareHandler))
	mux.Handle("PUT /api/draw/{id}/rename", auth.RequireAuth(canvas.RenameHandler))
	mux.Handle("POST /api/draw/{id}/thumbnail", auth.RequireAuth(canvas.ThumbnailHandler))

	// Shared (public read-only)
	mux.HandleFunc("GET /shared/{slug}", canvas.SharedPageHandler)
	mux.HandleFunc("GET /api/shared/{slug}/data", canvas.SharedDataHandler)

	// Profile
	mux.Handle("GET /profile", auth.RequireAuth(profile.PageHandler))

	// Docs
	mux.HandleFunc("GET /docs", docs.PageHandler)
	mux.HandleFunc("GET /docs/{slug}", docs.PageHandler)

	wrapped := auth.Middleware(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	addr := ":" + port
	fmt.Printf("Canvas running at http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, wrapped))
}
