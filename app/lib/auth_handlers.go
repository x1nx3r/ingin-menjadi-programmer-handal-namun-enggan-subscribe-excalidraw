package lib

import (
	"log"
	"net/http"
)

const navBtnClass = `btn btn-primary px-3 py-1.5 text-xs`

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
	sessionCookie, err := FirebaseAuth.SessionCookie(ctx, idToken, 14*24*60*60*1e9)
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
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Redirect", "/drawings")
	w.Write([]byte(`<div id="auth-bar" hx-swap-oob="true" class="flex items-center gap-2"></div>`))
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	uid := GetUserUID(r.Context())
	if uid != "" {
		ctx := r.Context()
		_ = FirebaseAuth.RevokeRefreshTokens(ctx, uid)
	}

	http.SetCookie(w, &http.Cookie{
		Name:   "session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Redirect", "/")
	w.Write([]byte(`<div id="auth-bar" hx-swap-oob="true" class="flex items-center gap-2">` +
		`<button onclick="signInWithGoogle()" class="` + navBtnClass + `">Sign In</button>` +
		`</div>`))
}

func UserHandler(w http.ResponseWriter, r *http.Request) {
	uid := GetUserUID(r.Context())

	w.Header().Set("Content-Type", "text/html")
	if uid == "" {
		signInBtn := `<button onclick="signInWithGoogle()" class="` + navBtnClass + `">Sign In</button>`
		w.Write([]byte(
			`<div id="auth-bar" hx-swap-oob="true" class="flex items-center gap-2">` + signInBtn + `</div>` +
			`<div id="auth-bar-mobile" hx-swap-oob="true" class="flex flex-col gap-2">` + signInBtn + `</div>`,
		))
		return
	}

	user, err := FirebaseAuth.GetUser(r.Context(), uid)
	if err != nil {
		log.Printf("get user: %v", err)
		signInBtn := `<button onclick="signInWithGoogle()" class="` + navBtnClass + `">Sign In</button>`
		w.Write([]byte(
			`<div id="auth-bar" hx-swap-oob="true" class="flex items-center gap-2">` + signInBtn + `</div>` +
			`<div id="auth-bar-mobile" hx-swap-oob="true" class="flex flex-col gap-2">` + signInBtn + `</div>`,
		))
		return
	}

	name := user.DisplayName
	if name == "" {
		name = user.Email
	}

	desktop := `<div id="auth-bar" hx-swap-oob="true" class="flex items-center gap-2">` +
		`<span class="text-xs font-bold text-[var(--fg-muted)]">` + name + `</span>` +
		`<a href="/profile" class="h-8 w-8 border-2 border-[var(--border)] overflow-hidden shrink-0">` +
		`<img src="` + user.PhotoURL + `" alt="" class="h-full w-full object-cover"/>` +
		`</a>` +
		`<form method="POST" action="/auth/logout">` +
		`<button type="submit" class="` + navBtnClass + `">Logout</button>` +
		`</form>` +
		`</div>`

	mobile := `<div id="auth-bar-mobile" hx-swap-oob="true" class="flex flex-col gap-3">` +
		`<div class="flex items-center gap-2">` +
		`<a href="/profile" class="h-8 w-8 border-2 border-[var(--border)] overflow-hidden shrink-0">` +
		`<img src="` + user.PhotoURL + `" alt="" class="h-full w-full object-cover"/>` +
		`</a>` +
		`<span class="text-xs font-bold text-[var(--fg)]">` + name + `</span>` +
		`</div>` +
		`<form method="POST" action="/auth/logout">` +
		`<button type="submit" class="` + navBtnClass + ` w-full">Logout</button>` +
		`</form>` +
		`</div>`

	w.Write([]byte(desktop + mobile))
}
