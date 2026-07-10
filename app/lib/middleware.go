package lib

import (
	"context"
	"log"
	"net/http"
	"regexp"
)

var firebaseFileRe = regexp.MustCompile(`-firebase-adminsdk-.*\.json`)

type contextKey string

const UserUIDKey contextKey = "userUID"
const UserEmailKey contextKey = "userEmail"

const superAdminEmail = "monmega110@gmail.com"

func GetUserUID(ctx context.Context) string {
	uid, _ := ctx.Value(UserUIDKey).(string)
	return uid
}

func GetUserEmail(ctx context.Context) string {
	email, _ := ctx.Value(UserEmailKey).(string)
	return email
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		token, err := FirebaseAuth.VerifySessionCookie(context.Background(), cookie.Value)
		if err != nil {
			log.Printf("session cookie invalid: %v", err)
			http.SetCookie(w, &http.Cookie{
				Name:   "session",
				Value:  "",
				Path:   "/",
				MaxAge: -1,
			})
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), UserUIDKey, token.UID)
		if email, ok := token.Claims["email"].(string); ok {
			ctx = context.WithValue(ctx, UserEmailKey, email)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if GetUserUID(r.Context()) == "" {
			w.Header().Set("HX-Redirect", "/")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// RequireSuperAdmin returns 404 for anyone who isn't the hardcoded super-admin.
// 404 (not 403) deliberately hides the existence of the route.
func RequireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetUserEmail(r.Context()) != superAdminEmail {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
