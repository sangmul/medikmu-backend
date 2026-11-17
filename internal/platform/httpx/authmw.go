package httpx

import (
	"net/http"
	"strings"

	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
)

// TokenVerifier validates an access token and returns the caller.
type TokenVerifier func(token string) (authctx.Principal, error)

// RequireAuth rejects requests without a valid Bearer access token and stores the caller in the
// request context (see authctx.From). Every module uses it to protect its routes.
func RequireAuth(verify TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				unauthorized(w)
				return
			}
			p, err := verify(token)
			if err != nil {
				unauthorized(w)
				return
			}
			next.ServeHTTP(w, r.WithContext(authctx.With(r.Context(), p)))
		})
	}
}

// RequireFacility additionally requires an active facility, optionally restricted to the given roles.
// Use it after RequireAuth on staff-only routes. Services still re-check authorization themselves.
func RequireFacility(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := authctx.From(r.Context())
			if !ok {
				unauthorized(w)
				return
			}
			if !p.HasFacility() || (len(roles) > 0 && !contains(roles, p.Role)) {
				Error(w, http.StatusForbidden, "forbidden", "Anda tidak memiliki akses.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="medikmu"`)
	Error(w, http.StatusUnauthorized, "unauthorized", "Silakan masuk terlebih dahulu.")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
