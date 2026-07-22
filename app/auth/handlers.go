package auth

import (
	"log"
	"net/http"

	"gotth/app/components"
	"gotth/app/lib"
	"gotth/app/middleware"
)

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	idToken := r.FormValue("idToken")
	if idToken == "" {
		http.Error(w, "missing idToken", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	sessionCookie, err := lib.FirebaseAuth.SessionCookie(ctx, idToken, 14*24*60*60*1e9)
	if err != nil {
		log.Printf("session cookie creation: %v", err)
		http.Error(w, "auth failed", http.StatusUnauthorized)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionCookie,
		Path:     "/",
		MaxAge:   14 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Redirect", "/drawings")
	w.Write([]byte(`<div id="auth-bar" hx-swap-oob="true"></div>`))
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserUID(r.Context())
	if uid != "" {
		_ = lib.FirebaseAuth.RevokeRefreshTokens(r.Context(), uid)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Redirect", "/")
	w.Write([]byte(`<div id="auth-bar" hx-swap-oob="true"></div>`))
}

func UserHandler(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserUID(r.Context())

	w.Header().Set("Content-Type", "text/html")
	if uid == "" {
		components.SignInBarDesktop().Render(r.Context(), w)
		components.SignInBarMobile().Render(r.Context(), w)
		return
	}

	user, err := lib.FirebaseAuth.GetUser(r.Context(), uid)
	if err != nil {
		log.Printf("get user: %v", err)
		components.SignInBarDesktop().Render(r.Context(), w)
		components.SignInBarMobile().Render(r.Context(), w)
		return
	}

	name := user.DisplayName
	if name == "" {
		name = user.Email
	}

	components.AuthBarDesktop(name, user.PhotoURL).Render(r.Context(), w)
	components.AuthBarMobile(name, user.PhotoURL).Render(r.Context(), w)
}
