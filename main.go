package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"gotth/app"
	"gotth/app/api"
	"gotth/app/assets"
	"gotth/app/canvas"
	"gotth/app/dashboard"
	"gotth/app/lib"
	"gotth/app/profile"
	_ "github.com/a-h/templ"
)

func main() {
	lib.InitAuth()

	dbPath := os.Getenv("SQLITE_DB_PATH")
	if dbPath == "" {
		dbPath = "./canvas.db"
	}
	lib.InitDB(dbPath)

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
	mux.HandleFunc("POST /auth/login", lib.LoginHandler)
	mux.HandleFunc("POST /auth/logout", lib.LogoutHandler)
	mux.HandleFunc("GET /auth/user", lib.UserHandler)

	// Pages
	mux.HandleFunc("GET /{$}", app.PageHandler)
	mux.Handle("GET /drawings", lib.RequireAuth(dashboard.PageHandler))
	mux.Handle("POST /draw/new", lib.RequireAuth(dashboard.NewHandler))
	mux.Handle("GET /draw/{id}", lib.RequireAuth(canvas.PageHandler))
	mux.Handle("GET /profile", lib.RequireAuth(profile.PageHandler))

	// API
	mux.Handle("GET /api/draw/{id}/data", lib.RequireAuth(api.DataHandler))
	mux.Handle("POST /api/draw/{id}/save", lib.RequireAuth(api.SaveHandler))
	mux.Handle("POST /api/draw/{id}/share", lib.RequireAuth(api.ShareHandler))
	mux.Handle("PUT /api/draw/{id}/rename", lib.RequireAuth(api.RenameHandler))
	mux.Handle("POST /api/draw/{id}/thumbnail", lib.RequireAuth(api.ThumbnailHandler))
	mux.Handle("DELETE /api/draw/{id}", lib.RequireAuth(api.DeleteHandler))

	// Shared (public)
	mux.HandleFunc("GET /shared/{slug}", canvas.SharedPageHandler)
	mux.HandleFunc("GET /api/shared/{slug}/data", api.SharedDataHandler)

	// Middleware
	wrapped := lib.Middleware(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	addr := ":" + port
	fmt.Printf("Canvas running at http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, wrapped))
}
