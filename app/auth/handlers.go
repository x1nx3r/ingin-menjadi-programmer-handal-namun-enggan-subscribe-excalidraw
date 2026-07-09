package auth

import (
	"html"
	"log"
	"net/http"
	"time"
)

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	idToken := r.FormValue("idToken")
	if idToken == "" {
		http.Error(w, "missing idToken", http.StatusBadRequest)
		return
	}

	cookie, err := FirebaseAuth.SessionCookie(r.Context(), idToken, 14*24*time.Hour)
	if err != nil {
		log.Printf("session cookie creation: %v", err)
		http.Error(w, "auth failed", http.StatusUnauthorized)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    cookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   false, // true in prod
		SameSite: http.SameSiteStrictMode,
		MaxAge:   14 * 24 * 60 * 60,
	})

	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	uid := GetUserUID(r.Context())
	if uid != "" {
		if err := FirebaseAuth.RevokeRefreshTokens(r.Context(), uid); err != nil {
			log.Printf("revoke error: %v", err)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})

	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusOK)
}

func UserHandler(w http.ResponseWriter, r *http.Request) {
	uid := GetUserUID(r.Context())
	if uid == "" {
		w.Write([]byte(`<div id="auth-bar" class="flex items-center gap-3"><button id="login-btn" onclick="signInWithGoogle()" class="px-3 py-1.5 border-2 border-[var(--border)] text-xs font-bold uppercase tracking-wider cursor-pointer hover:bg-[var(--bg-subtle)] transition-all">Sign in</button></div>`))
		return
	}

	user, err := FirebaseAuth.GetUser(r.Context(), uid)
	if err != nil {
		w.Write([]byte(`<div id="auth-bar">Error</div>`))
		return
	}

	name := user.DisplayName
	if name == "" {
		name = user.Email
	}
	w.Write([]byte(`<div id="auth-bar" class="flex items-center gap-3"><a href="/profile" class="text-xs font-bold text-[var(--fg-muted)] hover:text-[var(--fg)] transition-colors">` + html.EscapeString(name) + `</a><form hx-post="/auth/logout" hx-target="body" hx-swap="innerHTML"><button class="px-3 py-1.5 border-2 border-[var(--border)] text-xs font-bold uppercase tracking-wider cursor-pointer hover:bg-[var(--bg-subtle)] transition-all">Sign out</button></form></div>`))
}
