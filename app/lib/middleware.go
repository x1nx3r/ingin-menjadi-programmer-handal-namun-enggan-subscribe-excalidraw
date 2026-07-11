package lib

import (
	"context"
	"log"
	"net/http"
	"os"
	"regexp"
)

var firebaseFileRe = regexp.MustCompile(`-firebase-adminsdk-.*\.json`)

type contextKey string

const UserUIDKey contextKey = "userUID"
const UserEmailKey contextKey = "userEmail"

var superAdminEmail = func() string {
	if e := os.Getenv("SUPER_ADMIN_EMAIL"); e != "" {
		return e
	}
	return "monmega110@gmail.com"
}()

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

		uid := token.UID
		ctx := context.WithValue(r.Context(), UserUIDKey, uid)
		email, _ := token.Claims["email"].(string)
		if email != "" {
			ctx = context.WithValue(ctx, UserEmailKey, email)
		}
		name, _ := token.Claims["name"].(string)

		// Track the user in the admin panel's users table.
		_, err = DB.Exec(
			`INSERT INTO users (uid, email, name, created_at, last_seen)
			 VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			 ON CONFLICT(uid) DO UPDATE SET email=excluded.email, name=CASE WHEN excluded.name IS NOT NULL AND excluded.name != '' THEN excluded.name ELSE users.name END, last_seen=CURRENT_TIMESTAMP`,
			uid, email, name,
		)
		if err != nil {
			log.Printf("track user %s: %v", uid, err)
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
