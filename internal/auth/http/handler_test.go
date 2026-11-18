package authhttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sangmul/medikmu-backend/internal/auth/authtest"
	"github.com/sangmul/medikmu-backend/internal/auth/domain"
	authhttp "github.com/sangmul/medikmu-backend/internal/auth/http"
	"github.com/sangmul/medikmu-backend/internal/auth/service"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
	"github.com/sangmul/medikmu-backend/internal/platform/config"
	"github.com/sangmul/medikmu-backend/internal/platform/httpx"
)

type env struct {
	srv   http.Handler
	store *authtest.MemStore
	svc   *service.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := authtest.NewMemStore()
	svc, err := service.New(st, service.Config{
		JWTSecret: []byte("test-secret-test-secret-test-secret-123"),
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := authhttp.New(svc)
	staffOnly := httpx.RequireAuth(svc.VerifyAccessToken)(httpx.RequireFacility(domain.RoleDoctor)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _ := authctx.From(r.Context())
			httpx.JSON(w, http.StatusOK, map[string]string{"facility": p.FacilityID})
		})))
	srv := httpx.NewRouter(config.Config{}, h.Mount, func(mux *http.ServeMux) {
		mux.Handle("GET /api/v1/_test/staff", staffOnly)
	})
	return &env{srv: srv, store: st, svc: svc}
}

func (e *env) do(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec
}

type pair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func decodePair(t *testing.T, rec *httptest.ResponseRecorder) pair {
	t.Helper()
	var p pair
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return p
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return b.Error.Code
}

const registerBody = `{"email":"budi@example.com","password":"correct-horse","full_name":"Budi Santoso"}`

func TestFullFlow(t *testing.T) {
	e := newEnv(t)

	rec := e.do(t, "POST", "/api/v1/auth/register", "", registerBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("token responses must be Cache-Control: no-store")
	}
	reg := decodePair(t, rec)

	rec = e.do(t, "GET", "/api/v1/me", reg.AccessToken, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"email":"budi@example.com"`) {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("me must not expose password data: %s", rec.Body)
	}

	rec = e.do(t, "POST", "/api/v1/auth/login", "", `{"email":"budi@example.com","password":"correct-horse"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	login := decodePair(t, rec)

	rec = e.do(t, "POST", "/api/v1/auth/refresh", "", `{"refresh_token":"`+login.RefreshToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body)
	}
	next := decodePair(t, rec)

	// Old refresh token replayed -> 401 and the new one is revoked too.
	rec = e.do(t, "POST", "/api/v1/auth/refresh", "", `{"refresh_token":"`+login.RefreshToken+`"}`)
	if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "invalid_token" {
		t.Fatalf("reuse: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "POST", "/api/v1/auth/refresh", "", `{"refresh_token":"`+next.RefreshToken+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("family should be revoked after reuse: %d", rec.Code)
	}

	rec = e.do(t, "POST", "/api/v1/auth/logout", "", `{"refresh_token":"`+reg.RefreshToken+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}
}

func TestProfile(t *testing.T) {
	e := newEnv(t)
	reg := decodePair(t, e.do(t, "POST", "/api/v1/auth/register", "", registerBody))

	rec := e.do(t, "PUT", "/api/v1/me/profile", reg.AccessToken, `{"full_name":"Budi S.","phone":"0812-3456-7890"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"phone":"081234567890"`) ||
		!strings.Contains(rec.Body.String(), `"phone_verified":false`) || !strings.Contains(rec.Body.String(), `"full_name":"Budi S."`) {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do(t, "GET", "/api/v1/me", reg.AccessToken, ""); !strings.Contains(rec.Body.String(), `"phone":"081234567890"`) {
		t.Fatalf("/me must reflect the update: %s", rec.Body)
	}
	if rec = e.do(t, "PUT", "/api/v1/me/profile", reg.AccessToken, `{"full_name":"","phone":"1"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do(t, "PUT", "/api/v1/me/profile", reg.AccessToken, `{"full_name":"X","email":"hijack@evil.test"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("email is not editable here (unknown field): %d", rec.Code)
	}
	if rec = e.do(t, "PUT", "/api/v1/me/profile", "", `{"full_name":"X"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
}

func TestRegister_Errors(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/v1/auth/register", "", registerBody)

	tests := []struct {
		name, body string
		status     int
		code       string
	}{
		{"duplicate email", registerBody, http.StatusConflict, "email_taken"},
		{"validation", `{"email":"bad","password":"x","full_name":""}`, http.StatusUnprocessableEntity, "validation_failed"},
		{"malformed json", `{"email":`, http.StatusBadRequest, "invalid_request"},
		{"unknown field", `{"email":"a@b.co","password":"correct-horse","full_name":"A","role":"admin"}`, http.StatusBadRequest, "invalid_request"},
		{"trailing data", registerBody + `{}`, http.StatusBadRequest, "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do(t, "POST", "/api/v1/auth/register", "", tt.body)
			if rec.Code != tt.status || errCode(t, rec) != tt.code {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body, tt.status, tt.code)
			}
		})
	}
}

func TestLogin_WrongPasswordAndUnknownUserLookIdentical(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/v1/auth/register", "", registerBody)

	a := e.do(t, "POST", "/api/v1/auth/login", "", `{"email":"budi@example.com","password":"wrong-password"}`)
	b := e.do(t, "POST", "/api/v1/auth/login", "", `{"email":"ghost@example.com","password":"wrong-password"}`)
	if a.Code != http.StatusUnauthorized || a.Code != b.Code || a.Body.String() != b.Body.String() {
		t.Fatalf("responses differ:\n%d %s\n%d %s", a.Code, a.Body, b.Code, b.Body)
	}
}

func TestRateLimit_CredentialEndpointsOnly(t *testing.T) {
	st := authtest.NewMemStore()
	svc, err := service.New(st, service.Config{
		JWTSecret: []byte("test-secret-test-secret-test-secret-123"),
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	limiter := httpx.NewLimiter(1, 3) // burst of 3, then ~1/minute
	h := authhttp.New(svc, authhttp.WithRateLimit(limiter.Middleware(true)))
	e := &env{srv: httpx.NewRouter(config.Config{}, h.Mount), store: st, svc: svc}

	login := func(ip string) int {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"x@y.co","password":"wrong-password"}`))
		req.Header.Set("X-Forwarded-For", "6.6.6.6, "+ip) // left entry is attacker-controlled and ignored
		rec := httptest.NewRecorder()
		e.srv.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range 3 {
		if code := login("203.0.113.1"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401 (invalid creds), got %d", i, code)
		}
	}
	if code := login("203.0.113.1"); code != http.StatusTooManyRequests {
		t.Fatalf("4th attempt: want 429, got %d", code)
	}
	if code := login("203.0.113.2"); code != http.StatusUnauthorized {
		t.Fatalf("a different client IP must not be limited, got %d", code)
	}
	// Logout and /me are not credential-guessing endpoints and are not limited.
	rec := e.do(t, "POST", "/api/v1/auth/logout", "", `{"refresh_token":"x"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}
}

func TestProtectedRoutes_RequireValidBearer(t *testing.T) {
	e := newEnv(t)
	for name, token := range map[string]string{"none": "", "garbage": "abc.def.ghi"} {
		rec := e.do(t, "GET", "/api/v1/me", token, "")
		if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "unauthorized" {
			t.Errorf("%s: got %d %s", name, rec.Code, rec.Body)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: missing WWW-Authenticate", name)
		}
	}
}

func TestRequireFacility(t *testing.T) {
	e := newEnv(t)
	reg := decodePair(t, e.do(t, "POST", "/api/v1/auth/register", "", registerBody))

	// A plain patient session has no facility: forbidden, not unauthorized.
	if rec := e.do(t, "GET", "/api/v1/_test/staff", reg.AccessToken, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("patient on staff route: want 403, got %d", rec.Code)
	}

	// Become a doctor of fac-1 and switch into it.
	p, _ := e.svc.VerifyAccessToken(reg.AccessToken)
	e.store.AddMembership(p.UserID, "fac-1", domain.RoleDoctor)
	rec := e.do(t, "POST", "/api/v1/auth/switch-facility", reg.AccessToken,
		`{"refresh_token":"`+reg.RefreshToken+`","facility_id":"fac-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch: %d %s", rec.Code, rec.Body)
	}
	staff := decodePair(t, rec)
	if rec := e.do(t, "GET", "/api/v1/_test/staff", staff.AccessToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("doctor on staff route: want 200, got %d %s", rec.Code, rec.Body)
	}

	// Switching to a facility the user is not a member of is forbidden.
	rec = e.do(t, "POST", "/api/v1/auth/switch-facility", staff.AccessToken,
		`{"refresh_token":"`+staff.RefreshToken+`","facility_id":"fac-evil"}`)
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "forbidden" {
		t.Fatalf("foreign facility: %d %s", rec.Code, rec.Body)
	}
}
