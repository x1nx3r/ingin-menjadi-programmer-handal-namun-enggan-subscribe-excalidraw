package lib

import (
	"net/http"
	"sync"
	"time"
)

type rateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*bucket
	rate     int
	burst    int
	interval time.Duration
}

type bucket struct {
	tokens   int
	lastFill time.Time
}

func newRateLimiter(rate, burst int, interval time.Duration) *rateLimiter {
	return &rateLimiter{
		clients:  make(map[string]*bucket),
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
	refill := int(elapsed / rl.interval) * rl.rate
	b.tokens += refill
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false
}

var (
	authLimiter   = newRateLimiter(2, 5, time.Second)     // 2 req/s, burst 5
	wsLimiter     = newRateLimiter(1, 2, time.Second)     // 1 req/s, burst 2
	apiLimiter    = newRateLimiter(10, 20, time.Second)   // 10 req/s, burst 20
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

func RateLimitAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.RemoteAddr
		if !authLimiter.allow(key) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func RateLimitWS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.RemoteAddr
		if !wsLimiter.allow(key) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func RateLimitAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.RemoteAddr
		if !apiLimiter.allow(key) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
