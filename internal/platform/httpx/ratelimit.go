package httpx

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limiter is an in-memory token-bucket rate limiter keyed by string (usually the client IP).
// It is per process: fine for one dev/VPS instance. Move to Redis before running several instances.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   float64
	now     func() time.Time
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter allows perMinute requests per key on average, with bursts up to burst.
func NewLimiter(perMinute, burst int) *Limiter {
	return &Limiter{
		buckets: map[string]*bucket{},
		rate:    float64(perMinute) / 60,
		burst:   float64(burst),
		now:     time.Now,
	}
}

// Allow consumes one token for key. When it returns false, retryAfter says when to try again.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.gc(now)
	b, found := l.buckets[key]
	if !found {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// gc drops buckets that have been idle long enough to be full again, at most once a minute.
func (l *Limiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < time.Minute {
		return
	}
	l.lastGC = now
	idle := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) > idle {
			delete(l.buckets, k)
		}
	}
}

// Middleware rejects requests over the limit with 429 and a Retry-After header.
// Set trustProxy only when the API sits behind a proxy you control (ngrok, Caddy): the client IP is then
// taken from the right-most X-Forwarded-For entry, which that proxy appended. Never trust it otherwise.
func (l *Limiter) Middleware(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, wait := l.Allow(ClientIP(r, trustProxy))
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
				Error(w, http.StatusTooManyRequests, "rate_limited", "Terlalu banyak percobaan. Coba lagi sebentar lagi.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the caller's IP. See Limiter.Middleware for when trustProxy is safe.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
