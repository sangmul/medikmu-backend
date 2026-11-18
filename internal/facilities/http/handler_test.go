package facilitieshttp_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sangmul/medikmu-backend/internal/facilities/domain"
	"github.com/sangmul/medikmu-backend/internal/facilities/facilitiestest"
	facilitieshttp "github.com/sangmul/medikmu-backend/internal/facilities/http"
	"github.com/sangmul/medikmu-backend/internal/facilities/service"
	"github.com/sangmul/medikmu-backend/internal/platform/audit"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
	"github.com/sangmul/medikmu-backend/internal/platform/config"
	"github.com/sangmul/medikmu-backend/internal/platform/httpx"
)

// env uses opaque test tokens ("tok:<name>") mapped to principals, so these tests cover the handler layer
// without the auth module. The real auth -> facilities -> patients flow is exercised against Postgres.
type env struct {
	srv        http.Handler
	store      *facilitiestest.MemStore
	principals map[string]authctx.Principal
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{store: facilitiestest.NewMemStore(), principals: map[string]authctx.Principal{}}
	verify := func(tok string) (authctx.Principal, error) {
		if p, ok := e.principals[tok]; ok {
			return p, nil
		}
		return authctx.Principal{}, errors.New("bad token")
	}
	h := facilitieshttp.New(service.New(e.store, &audit.Memory{}), verify)
	e.srv = httpx.NewRouter(config.Config{}, h.Mount)
	return e
}

// user registers an account and returns its token (personal context, no facility).
func (e *env) user(name, email string) (token, userID string) {
	userID = e.store.AddUser(name, email)
	token = "tok:" + name
	e.principals[token] = authctx.Principal{UserID: userID}
	return token, userID
}

// switchTo returns a token scoped to a facility, as POST /auth/switch-facility would issue.
func (e *env) switchTo(name, userID, facility, role string) string {
	token := "tok:" + name + ":" + facility
	e.principals[token] = authctx.Principal{UserID: userID, FacilityID: facility, Role: role}
	return token
}

func (e *env) do(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct{ Error struct{ Code string } }
	decode(t, rec, &b)
	return b.Error.Code
}

const clinicBody = `{"name":"Klinik Sehat Bersama","kind":"clinic","phone":"021 555 0100","address":"Jl. Merdeka 1"}`

func TestFullOnboardingFlow(t *testing.T) {
	e := newEnv(t)
	ownerTok, ownerID := e.user("owner", "owner@example.com")

	// 1. A plain account creates a facility and becomes its admin.
	rec := e.do("POST", "/api/v1/facilities", ownerTok, clinicBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create facility: %d %s", rec.Code, rec.Body)
	}
	var f struct{ ID, Name, Kind string }
	decode(t, rec, &f)
	adminTok := e.switchTo("owner", ownerID, f.ID, domain.RoleAdmin)

	// 2. Admin reads and edits the profile.
	if rec = e.do("GET", "/api/v1/facilities/current", adminTok, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Klinik Sehat Bersama") {
		t.Fatalf("current: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do("PUT", "/api/v1/facilities/current", adminTok, `{"name":"Klinik Sehat Utama","phone":"0215550199","address":"Jl. Baru 2"}`); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// 3. Admin invites a doctor; the token comes back once, never in the list.
	rec = e.do("POST", "/api/v1/invites", adminTok, `{"email":"dokter@example.com","role":"doctor"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("invite response must be no-store")
	}
	var inv struct{ ID, Token string }
	decode(t, rec, &inv)
	if inv.Token == "" {
		t.Fatal("create invite must return the one-time token")
	}
	if rec = e.do("GET", "/api/v1/invites", adminTok, ""); strings.Contains(rec.Body.String(), inv.Token) || !strings.Contains(rec.Body.String(), "dokter@example.com") {
		t.Fatalf("list must show the invite but never the token: %s", rec.Body)
	}

	// 4. The doctor (a plain account) accepts and is then a doctor of the facility.
	docTok, docID := e.user("dokter", "dokter@example.com")
	rec = e.do("POST", "/api/v1/invites/accept", docTok, `{"token":"`+inv.Token+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"role":"doctor"`) || !strings.Contains(rec.Body.String(), f.ID) {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do("POST", "/api/v1/invites/accept", docTok, `{"token":"`+inv.Token+`"}`); rec.Code != http.StatusBadRequest || errCode(t, rec) != "invite_invalid" {
		t.Fatalf("reuse: %d %s", rec.Code, rec.Body)
	}

	// 5. Team: admin sees both; the doctor may read the facility but not the team.
	if rec = e.do("GET", "/api/v1/team", adminTok, ""); !strings.Contains(rec.Body.String(), "dokter@example.com") || !strings.Contains(rec.Body.String(), "owner@example.com") {
		t.Fatalf("team: %d %s", rec.Code, rec.Body)
	}
	docInFacility := e.switchTo("dokter", docID, f.ID, domain.RoleDoctor)
	if rec = e.do("GET", "/api/v1/facilities/current", docInFacility, ""); rec.Code != http.StatusOK {
		t.Fatalf("doctor reads facility: %d", rec.Code)
	}
	if rec = e.do("GET", "/api/v1/team", docInFacility, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("doctor on /team: want 403, got %d", rec.Code)
	}

	// 6. Admin removes the doctor, then cannot remove themselves (last admin).
	if rec = e.do("DELETE", "/api/v1/team/"+docID, adminTok, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do("DELETE", "/api/v1/team/"+ownerID, adminTok, ""); rec.Code != http.StatusConflict || errCode(t, rec) != "last_admin" {
		t.Fatalf("last admin: %d %s", rec.Code, rec.Body)
	}
}

func TestAuthRequired(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/facilities"}, {"GET", "/api/v1/facilities/current"}, {"GET", "/api/v1/team"},
		{"POST", "/api/v1/invites"}, {"GET", "/api/v1/invites"}, {"POST", "/api/v1/invites/accept"},
	} {
		if rec := e.do(c.method, c.path, "", "{}"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token: want 401, got %d", c.method, c.path, rec.Code)
		}
	}
}

func TestRequestErrors(t *testing.T) {
	e := newEnv(t)
	tok, uid := e.user("owner", "owner@example.com")
	rec := e.do("POST", "/api/v1/facilities", tok, clinicBody)
	var f struct{ ID string }
	decode(t, rec, &f)
	admin := e.switchTo("owner", uid, f.ID, domain.RoleAdmin)

	tests := []struct {
		name, method, path, token, body string
		status                          int
		code                            string
	}{
		{"validation", "POST", "/api/v1/facilities", tok, `{"name":"x","kind":"zoo"}`, 422, "validation_failed"},
		{"unknown field", "POST", "/api/v1/facilities", tok, `{"name":"Klinik Baru","kind":"clinic","created_by":"me"}`, 400, "invalid_request"},
		{"kind not editable", "PUT", "/api/v1/facilities/current", admin, `{"name":"Klinik Baru","kind":"hospital"}`, 400, "invalid_request"},
		{"malformed", "POST", "/api/v1/invites", admin, `{"email":`, 400, "invalid_request"},
		{"bad role", "POST", "/api/v1/invites", admin, `{"email":"a@example.com","role":"root"}`, 422, "validation_failed"},
		{"already member", "POST", "/api/v1/invites", admin, `{"email":"owner@example.com","role":"doctor"}`, 409, "already_member"},
		{"revoke unknown", "DELETE", "/api/v1/invites/00000000-0000-4000-8000-0000000000ff", admin, "", 404, "not_found"},
		{"remove bad id", "DELETE", "/api/v1/team/xyz", admin, "", 404, "not_found"},
		{"accept as stranger", "POST", "/api/v1/invites/accept", tok, `{"token":"nope"}`, 400, "invite_invalid"},
		{"patient session on /team", "GET", "/api/v1/team", tok, "", 403, "forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do(tt.method, tt.path, tt.token, tt.body)
			if rec.Code != tt.status || errCode(t, rec) != tt.code {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body, tt.status, tt.code)
			}
		})
	}
}

func TestFacilityLimitMapsTo409(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.user("owner", "owner@example.com")
	for range domain.MaxFacilitiesPerUser {
		if rec := e.do("POST", "/api/v1/facilities", tok, clinicBody); rec.Code != http.StatusCreated {
			t.Fatalf("setup: %d", rec.Code)
		}
	}
	if rec := e.do("POST", "/api/v1/facilities", tok, clinicBody); rec.Code != http.StatusConflict || errCode(t, rec) != "facility_limit" {
		t.Fatalf("over the cap: %d %s", rec.Code, rec.Body)
	}
}
