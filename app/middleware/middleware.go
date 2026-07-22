package middleware

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"gotth/app/lib"
)

// --- Context keys & accessors ---

type contextKey string

const UserUIDKey contextKey = "userUID"
const UserEmailKey contextKey = "userEmail"

func GetUserUID(ctx context.Context) string {
	uid, _ := ctx.Value(UserUIDKey).(string)
	return uid
}

func GetUserEmail(ctx context.Context) string {
	email, _ := ctx.Value(UserEmailKey).(string)
	return email
}

// --- Session middleware ---

// Middleware verifies the session cookie on every request and injects the
// authenticated user's UID and email into the request context.
// Unauthenticated requests pass through unmodified.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		token, err := lib.FirebaseAuth.VerifySessionCookie(context.Background(), cookie.Value)
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
		clientIP := RealIP(r)
		_, err = lib.DB.Exec(
			`INSERT INTO users (uid, email, name, created_at, last_seen, last_ip)
			 VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?)
			 ON CONFLICT(uid) DO UPDATE SET
			   email = excluded.email,
			   name = CASE WHEN excluded.name IS NOT NULL AND excluded.name != '' THEN excluded.name ELSE users.name END,
			   last_seen = CURRENT_TIMESTAMP,
			   last_ip = ?`,
			uid, email, name, clientIP, clientIP,
		)
		if err != nil {
			log.Printf("track user %s: %v", uid, err)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// --- Auth guards ---

var superAdminEmail = func() string {
	if e := os.Getenv("SUPER_ADMIN_EMAIL"); e != "" {
		return e
	}
	return "monmega110@gmail.com"
}()

// RequireAuth rejects unauthenticated requests with a 401 + HX-Redirect so
// HTMX on the client handles the redirect without a hard navigation.
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

// RequireSuperAdmin returns 404 for anyone who isn't the super-admin.
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

// --- Rate limiting ---

type rateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*bucket
	rate     int
	burst    int
	interval time.Duration
	hits     int64
	blocked  map[string]int64
}

type bucket struct {
	tokens   int
	lastFill time.Time
}

func newRateLimiter(rate, burst int, interval time.Duration) *rateLimiter {
	return &rateLimiter{
		clients:  make(map[string]*bucket),
		blocked:  make(map[string]int64),
		rate:     rate,
		burst:    burst,
		interval: interval,
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.clients[key]
	if !ok {
		b = &bucket{tokens: rl.burst - 1, lastFill: time.Now()}
		rl.clients[key] = b
		return true
	}

	elapsed := time.Since(b.lastFill)
	b.lastFill = time.Now()
	refill := int(elapsed/rl.interval) * rl.rate
	b.tokens += refill
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}
	rl.hits++
	rl.blocked[key]++
	return false
}

// RateLimitReport is a snapshot of a single limiter's state for the admin panel.
type RateLimitReport struct {
	Name    string
	Hits    int64
	Rate    int
	Burst   int
	Blocked map[string]int64
}

func (rl *rateLimiter) report(name string) RateLimitReport {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	blocked := make(map[string]int64, len(rl.blocked))
	for k, v := range rl.blocked {
		blocked[k] = v
	}
	return RateLimitReport{
		Name:    name,
		Hits:    rl.hits,
		Rate:    rl.rate,
		Burst:   rl.burst,
		Blocked: blocked,
	}
}

var (
	authLimiter   = newRateLimiter(2, 5, time.Second)   // 2 req/s, burst 5
	wsLimiter     = newRateLimiter(1, 2, time.Second)   // 1 req/s, burst 2
	apiLimiter    = newRateLimiter(10, 20, time.Second) // 10 req/s, burst 20
	cleanupTicker = time.NewTicker(5 * time.Minute)
)

func init() {
	go func() {
		for range cleanupTicker.C {
			authLimiter.mu.Lock()
			authLimiter.clients = make(map[string]*bucket)
			authLimiter.mu.Unlock()
			wsLimiter.mu.Lock()
			wsLimiter.clients = make(map[string]*bucket)
			wsLimiter.mu.Unlock()
			apiLimiter.mu.Lock()
			apiLimiter.clients = make(map[string]*bucket)
			apiLimiter.mu.Unlock()
		}
	}()
}

// RealIP extracts the real client IP, honouring Cloudflare's header first.
func RealIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// RateLimitReports returns a snapshot of all limiters for the admin panel.
func RateLimitReports() []RateLimitReport {
	return []RateLimitReport{
		authLimiter.report("auth"),
		wsLimiter.report("ws"),
		apiLimiter.report("api"),
	}
}

func RateLimitAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authLimiter.allow(RealIP(r)) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func RateLimitWS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !wsLimiter.allow(RealIP(r)) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func RateLimitAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiLimiter.allow(RealIP(r)) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
