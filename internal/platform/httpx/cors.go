package httpx

import (
	"net/http"
	"strings"
)

// corsAllowedHeaders includes ngrok-skip-browser-warning: ngrok's free tier shows an interstitial page to
// browsers unless the request carries that header, so web clients sent through ngrok need it allowed.
const corsAllowedHeaders = "Authorization, Content-Type, Idempotency-Key, ngrok-skip-browser-warning"

// CORS allows cross-origin browser calls from the exact origins listed (no wildcards: the API uses
// Authorization headers). Requests without an Origin header (mobile apps, curl) pass through untouched.
// A preflight from an unlisted origin gets 403; a normal request from one simply gets no CORS headers.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			allowed[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Add("Vary", "Origin")
			isPreflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""

			if !allowed[origin] {
				if isPreflight {
					Error(w, http.StatusForbidden, "cors_forbidden", "Origin tidak diizinkan.")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", "Location, Retry-After")
			if isPreflight {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
