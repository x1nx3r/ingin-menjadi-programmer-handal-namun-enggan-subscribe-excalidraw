package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gotth/app"
	"gotth/app/admin"
	"gotth/app/api"
	"gotth/app/assets"
	"gotth/app/auth"
	"gotth/app/canvas"
	"gotth/app/dashboard"
	"gotth/app/lib"
	"gotth/app/middleware"
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

	// Globals CSS (embedded binary) with cache busting
	mux.Handle("GET /globals.css", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", `"`+assets.CSSHash+`"`)
		w.Write(assets.CSS)
	}))

	// Static assets (embedded under public/)
	publicFS, err := fs.Sub(assets.Public, "public")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(publicFS))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add cache busting for CSS files
		if len(r.URL.Path) > 4 && r.URL.Path[len(r.URL.Path)-4:] == ".css" {
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		}
		fileServer.ServeHTTP(w, r)
	})))

	// Auth
	mux.Handle("POST /auth/login", middleware.RateLimitAuth(auth.LoginHandler))
	mux.Handle("POST /auth/logout", middleware.RateLimitAuth(auth.LogoutHandler))
	mux.HandleFunc("GET /auth/user", auth.UserHandler)

	// Pages
	mux.HandleFunc("GET /{$}", app.PageHandler)
	mux.Handle("GET /drawings", middleware.RequireAuth(dashboard.PageHandler))
	mux.Handle("POST /draw/new", middleware.RequireAuth(dashboard.NewHandler))
	mux.Handle("GET /draw/{id}", middleware.RequireAuth(canvas.PageHandler))
	mux.Handle("GET /profile", middleware.RequireAuth(profile.PageHandler))

	// API
	mux.Handle("GET /api/draw/{id}/data", middleware.RequireAuth(api.DataHandler))
	mux.Handle("POST /api/draw/{id}/save", middleware.RequireAuth(middleware.RateLimitAPI(api.SaveHandler)))
	mux.Handle("POST /api/draw/{id}/share", middleware.RequireAuth(middleware.RateLimitAPI(api.ShareHandler)))
	mux.Handle("PUT /api/draw/{id}/rename", middleware.RequireAuth(middleware.RateLimitAPI(api.RenameHandler)))
	mux.Handle("POST /api/draw/{id}/thumbnail", middleware.RequireAuth(middleware.RateLimitAPI(api.ThumbnailHandler)))
	mux.Handle("PUT /api/draw/{id}/public-edit", middleware.RequireAuth(middleware.RateLimitAPI(api.PublicEditHandler)))
	mux.Handle("POST /api/draw/{id}/file", middleware.RequireAuth(middleware.RateLimitAPI(api.SaveFileHandler)))
	mux.Handle("DELETE /api/draw/{id}/file", middleware.RequireAuth(middleware.RateLimitAPI(api.DeleteFileHandler)))
	mux.Handle("DELETE /api/draw/{id}", middleware.RequireAuth(middleware.RateLimitAPI(api.DeleteHandler)))

	// Public share endpoints are rate-limited to discourage brute-force slug scanning.
	mux.Handle("GET /shared/{slug}", middleware.RateLimitAPI(canvas.SharedPageHandler))
	mux.Handle("GET /api/shared/{slug}/data", middleware.RateLimitAPI(api.SharedDataHandler))

	// File serving — accessible by owner OR any shared drawing viewer.
	mux.Handle("GET /api/file/{drawingId}/{fileId}", middleware.RateLimitAPI(api.ServeFileHandler))

	// WebSocket routes are rate-limited per-IP to prevent connection exhaustion.
	mux.Handle("GET /api/draw/{id}/ws", middleware.RequireAuth(middleware.RateLimitWS(api.OwnerWSHandler)))
	mux.Handle("GET /api/draw/{id}/collab-status", middleware.RequireAuth(middleware.RateLimitWS(api.CollabStatusHandler)))
	mux.Handle("GET /api/draw/{id}/collab-events", middleware.RequireAuth(middleware.RateLimitWS(api.CollabEventsHandler)))
	mux.Handle("GET /api/shared/{slug}/ws", middleware.RateLimitWS(api.GuestWSHandler))
	mux.Handle("GET /api/ws/stats", middleware.RequireSuperAdmin(http.HandlerFunc(api.WsStatsHandler)))

	// SEO: robots.txt
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("User-agent: *\nAllow: /$\nAllow: /shared/\nDisallow: /admin/\nDisallow: /api/\nDisallow: /auth/\nDisallow: /draw/\nDisallow: /drawings\nDisallow: /profile\nSitemap: https://canvas.x1nx3r.dev/sitemap.xml\n"))
	})

	// SEO: sitemap.xml
	mux.HandleFunc("GET /sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://canvas.x1nx3r.dev/</loc><priority>1.0</priority></url>
</urlset>`))
	})

	// Super-admin panel (404 for everyone else)
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /admin", admin.PageHandler)
	adminMux.HandleFunc("GET /admin/", admin.PageHandler)
	adminMux.HandleFunc("GET /admin/users", admin.PageHandler)
	adminMux.HandleFunc("GET /admin/users/{uid}", admin.PageHandler)
	adminMux.HandleFunc("GET /admin/drawings", admin.PageHandler)
	adminMux.HandleFunc("GET /admin/hub", admin.PageHandler)
	adminMux.HandleFunc("GET /admin/system", admin.PageHandler)
	adminMux.HandleFunc("GET /admin/vip", admin.PageHandler)
	adminMux.HandleFunc("POST /admin/vip/add", admin.AddHandler)
	adminMux.HandleFunc("DELETE /admin/vip/remove", admin.RemoveHandler)
	adminMux.HandleFunc("POST /admin/users/storage-unlimited-toggle", admin.PageHandler)
	mux.Handle("/admin/", middleware.RequireSuperAdmin(adminMux))

	if os.Getenv("SUPER_ADMIN_EMAIL") == "" {
		log.Fatal("SUPER_ADMIN_EMAIL environment variable is required")
	}

	// Middleware
	wrapped := middleware.Middleware(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	addr := ":" + port

	srv := &http.Server{
		Addr:              addr,
		Handler:           wrapped,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("shutting down...")

		// Stop the WAL checkpoint goroutine.
		lib.StopWAL()

		// Force-close all WebSocket connections so their goroutines exit.
		api.ShutdownHub()

		// Drain HTTP connections with a deadline.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}()

	log.Printf("Canvas running at http://localhost%s\n", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
