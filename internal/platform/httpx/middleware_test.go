package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sangmul/medikmu-backend/internal/platform/config"
)

func ok() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestCORS(t *testing.T) {
	h := CORS([]string{"https://web.example.com/", "http://localhost:5173"})(ok())

	do := func(method, origin string, preflight bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/patients", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if preflight {
			req.Header.Set("Access-Control-Request-Method", "POST")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("no Origin (mobile, curl) is untouched", func(t *testing.T) {
		rec := do("GET", "", false)
		if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("%d %v", rec.Code, rec.Header())
		}
	})
	t.Run("allowed origin gets exact echo, trailing slash in config ignored", func(t *testing.T) {
		rec := do("GET", "https://web.example.com", false)
		if rec.Header().Get("Access-Control-Allow-Origin") != "https://web.example.com" || rec.Header().Get("Vary") != "Origin" {
			t.Fatalf("headers: %v", rec.Header())
		}
	})
	t.Run("preflight allows ngrok header and answers 204", func(t *testing.T) {
		rec := do("OPTIONS", "http://localhost:5173", true)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Headers"); got != corsAllowedHeaders {
			t.Fatalf("allow-headers = %q", got)
		}
		for _, want := range []string{"Authorization", "ngrok-skip-browser-warning"} {
			if !containsStr(rec.Header().Get("Access-Control-Allow-Headers"), want) {
				t.Errorf("preflight must allow %s", want)
			}
		}
	})
	t.Run("unlisted origin: no CORS headers, preflight 403", func(t *testing.T) {
		rec := do("GET", "https://evil.example.com", false)
		if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("evil origin must get no CORS headers: %v", rec.Header())
		}
		if rec := do("OPTIONS", "https://evil.example.com", true); rec.Code != http.StatusForbidden {
			t.Fatalf("evil preflight: %d", rec.Code)
		}
	})
	t.Run("prefix/suffix tricks do not match", func(t *testing.T) {
		for _, o := range []string{"https://web.example.com.evil.io", "http://web.example.com", "https://xweb.example.com", "null"} {
			if rec := do("GET", o, false); rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("origin %q must not be allowed", o)
			}
		}
	})
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRouterCORSPreflightBypassesMux405(t *testing.T) {
	// A preflight to a route that only has GET/POST handlers must still succeed.
	h := NewRouter(config.Config{CORSAllowedOrigins: []string{"https://web.example.com"}}, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /api/v1/thing", func(http.ResponseWriter, *http.Request) {})
	})
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/thing", nil)
	req.Header.Set("Origin", "https://web.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight through router: %d", rec.Code)
	}
}

func TestLimiter_BurstThenRefill(t *testing.T) {
	l := NewLimiter(60, 3) // 1 token/second, burst 3
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("request %d within burst was denied", i)
		}
	}
	ok1, wait := l.Allow("1.2.3.4")
	if ok1 || wait <= 0 || wait > time.Second {
		t.Fatalf("4th request: ok=%v wait=%v (want denied, <=1s)", ok1, wait)
	}
	if ok2, _ := l.Allow("9.9.9.9"); !ok2 {
		t.Fatal("another key must have its own bucket")
	}
	now = now.Add(1100 * time.Millisecond) // one token refilled
	if ok3, _ := l.Allow("1.2.3.4"); !ok3 {
		t.Fatal("token should have refilled after 1s")
	}
	if ok4, _ := l.Allow("1.2.3.4"); ok4 {
		t.Fatal("only one token should have refilled")
	}
}

func TestLimiter_GCDropsIdleBuckets(t *testing.T) {
	l := NewLimiter(60, 2)
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	for i := range 100 {
		l.Allow(string(rune('a'+i%26)) + string(rune('A'+i/26)))
	}
	now = now.Add(10 * time.Minute)
	l.Allow("trigger-gc")
	if len(l.buckets) != 1 {
		t.Fatalf("idle buckets should be collected, have %d", len(l.buckets))
	}
}

func TestLimiterMiddleware_429(t *testing.T) {
	l := NewLimiter(60, 2)
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	h := l.Middleware(false)(ok())

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
		req.RemoteAddr = "10.0.0.1:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	do()
	do()
	rec := do()
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("got %d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	// A client prepends a fake address; the proxy (ngrok) appends the real one on the right.
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")

	if got := ClientIP(req, false); got != "10.0.0.1" {
		t.Errorf("untrusted: want socket IP, got %s", got)
	}
	if got := ClientIP(req, true); got != "203.0.113.9" {
		t.Errorf("trusted proxy: want right-most entry (spoofed left entry ignored), got %s", got)
	}
	req.Header.Del("X-Forwarded-For")
	if got := ClientIP(req, true); got != "10.0.0.1" {
		t.Errorf("no header: want socket IP, got %s", got)
	}
}

func TestReadyz(t *testing.T) {
	for name, tt := range map[string]struct {
		err  error
		code int
	}{"up": {nil, 200}, "db down": {errors.New("connection refused"), 503}} {
		t.Run(name, func(t *testing.T) {
			h := NewRouter(config.Config{}, Readyz(func(context.Context) error { return tt.err }))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
			if rec.Code != tt.code {
				t.Fatalf("got %d, want %d", rec.Code, tt.code)
			}
			if tt.err != nil && containsStr(rec.Body.String(), "refused") {
				t.Fatal("readyz must not leak the underlying error")
			}
		})
	}
}
