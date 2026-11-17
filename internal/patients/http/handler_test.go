package patientshttp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sangmul/medikmu-backend/internal/auth/authtest"
	authdomain "github.com/sangmul/medikmu-backend/internal/auth/domain"
	authservice "github.com/sangmul/medikmu-backend/internal/auth/service"
	patientshttp "github.com/sangmul/medikmu-backend/internal/patients/http"
	"github.com/sangmul/medikmu-backend/internal/patients/patientstest"
	"github.com/sangmul/medikmu-backend/internal/patients/service"
	"github.com/sangmul/medikmu-backend/internal/platform/audit"
	"github.com/sangmul/medikmu-backend/internal/platform/config"
	"github.com/sangmul/medikmu-backend/internal/platform/httpx"
)

const (
	facA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	facB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

// env wires the real auth service (for real JWTs) to the patients module over HTTP.
type env struct {
	srv       http.Handler
	authStore *authtest.MemStore
	auth      *authservice.Service
	audit     *audit.Memory
}

func newEnv(t *testing.T) *env {
	t.Helper()
	as := authtest.NewMemStore()
	auth, err := authservice.New(as, authservice.Config{
		JWTSecret: []byte("test-secret-test-secret-test-secret-123"),
		AccessTTL: time.Hour, RefreshTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	au := &audit.Memory{}
	h := patientshttp.New(service.New(patientstest.NewMemStore(), au), auth.VerifyAccessToken)
	return &env{srv: httpx.NewRouter(config.Config{}, h.Mount), authStore: as, auth: auth, audit: au}
}

// tokenFor registers a user, grants them role in facility, and returns an access token scoped to it.
func (e *env) tokenFor(t *testing.T, email, facility, role string) string {
	t.Helper()
	ctx := t.Context()
	pair, err := e.auth.Register(ctx, authservice.RegisterInput{Email: email, Password: "correct-horse", FullName: "Staf"})
	if err != nil {
		t.Fatal(err)
	}
	if facility == "" {
		return pair.AccessToken // personal (patient) context
	}
	p, _ := e.auth.VerifyAccessToken(pair.AccessToken)
	e.authStore.AddMembership(p.UserID, facility, role)
	pair, err = e.auth.SwitchFacility(ctx, p.UserID, pair.RefreshToken, facility)
	if err != nil {
		t.Fatal(err)
	}
	return pair.AccessToken
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

const createBody = `{
  "full_name": "Siti Aminah", "sex": "F", "birth_date": "1990-04-12",
  "nik": "3174012345678901", "phone": "0812-3456-7890",
  "payment_type": "bpjs", "bpjs_number": "0001234567890",
  "allergies": ["Penicillin"], "notes": "rahasia klinis", "consent": true
}`

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

func TestCRUDFlow(t *testing.T) {
	e := newEnv(t)
	tok := e.tokenFor(t, "dokter@example.com", facA, authdomain.RoleDoctor)

	rec := e.do("POST", "/api/v1/patients", tok, createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("patient data must be Cache-Control: no-store")
	}
	var created struct {
		ID, MRN, NIK, Notes string
		Phone               string `json:"phone"`
		Allergies           []string
	}
	decode(t, rec, &created)
	if created.MRN != "RM-000001" || created.NIK != "3174012345678901" || created.Phone != "081234567890" {
		t.Fatalf("unexpected create response: %s", rec.Body)
	}
	if rec.Header().Get("Location") != "/api/v1/patients/"+created.ID {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}

	rec = e.do("GET", "/api/v1/patients/"+created.ID, tok, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"nik":"3174012345678901"`) {
		t.Fatalf("detail should show full NIK to staff: %d %s", rec.Code, rec.Body)
	}

	rec = e.do("PUT", "/api/v1/patients/"+created.ID, tok, `{
	  "full_name": "Siti Aminah Putri", "sex": "F", "birth_date": "1990-04-12",
	  "phone": "0812-3456-7890", "payment_type": "private"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Siti Aminah Putri") {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// Only admins may archive.
	if rec = e.do("DELETE", "/api/v1/patients/"+created.ID, tok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("doctor archive: want 403, got %d", rec.Code)
	}
	admin := e.tokenFor(t, "admin@example.com", facA, authdomain.RoleAdmin)
	if rec = e.do("DELETE", "/api/v1/patients/"+created.ID, admin, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("admin archive: %d %s", rec.Code, rec.Body)
	}
	if rec = e.do("GET", "/api/v1/patients/"+created.ID, tok, ""); rec.Code != http.StatusNotFound || errCode(t, rec) != "patient_not_found" {
		t.Fatalf("archived: %d %s", rec.Code, rec.Body)
	}
}

func TestList_MasksNIKAndOmitsClinicalFields(t *testing.T) {
	e := newEnv(t)
	tok := e.tokenFor(t, "dokter@example.com", facA, authdomain.RoleDoctor)
	e.do("POST", "/api/v1/patients", tok, createBody)

	rec := e.do("GET", "/api/v1/patients", tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"nik_masked":"3174 **** **** 8901"`) {
		t.Errorf("want masked NIK in list: %s", body)
	}
	for _, leak := range []string{"3174012345678901", "rahasia klinis", `"notes"`, `"address"`, `"bpjs_number"`} {
		if strings.Contains(body, leak) {
			t.Errorf("list must not expose %q: %s", leak, body)
		}
	}
}

func TestPagination(t *testing.T) {
	e := newEnv(t)
	tok := e.tokenFor(t, "dokter@example.com", facA, authdomain.RoleDoctor)
	for i := range 5 {
		body := fmt.Sprintf(`{"full_name":"Pasien %d","sex":"M","birth_date":"1990-01-01","phone":"081234567890","payment_type":"private","consent":true}`, i)
		if rec := e.do("POST", "/api/v1/patients", tok, body); rec.Code != http.StatusCreated {
			t.Fatalf("seed: %d %s", rec.Code, rec.Body)
		}
	}

	seen := map[string]bool{}
	path := "/api/v1/patients?limit=2"
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
		rec := e.do("GET", path, tok, "")
		var page struct {
			Data       []struct{ ID string }
			NextCursor *string `json:"next_cursor"`
		}
		decode(t, rec, &page)
		for _, p := range page.Data {
			if seen[p.ID] {
				t.Fatalf("duplicate across pages: %s", p.ID)
			}
			seen[p.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		path = "/api/v1/patients?limit=2&cursor=" + *page.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("want 5 distinct patients across pages, got %d", len(seen))
	}
}

func TestBadQueryParams(t *testing.T) {
	e := newEnv(t)
	tok := e.tokenFor(t, "dokter@example.com", facA, authdomain.RoleDoctor)
	for _, q := range []string{"limit=0", "limit=101", "limit=abc", "cursor=!!!", "cursor=" + "bm90LWEtY3Vyc29y"} {
		if rec := e.do("GET", "/api/v1/patients?"+q, tok, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %s", q, rec.Code, rec.Body)
		}
	}
}

func TestAuthAndTenancy(t *testing.T) {
	e := newEnv(t)
	docA := e.tokenFor(t, "a@example.com", facA, authdomain.RoleDoctor)
	docB := e.tokenFor(t, "b@example.com", facB, authdomain.RoleDoctor)
	patient := e.tokenFor(t, "pasien@example.com", "", "")

	var created struct{ ID string }
	decode(t, e.do("POST", "/api/v1/patients", docA, createBody), &created)

	// No token / garbage token.
	for _, tok := range []string{"", "garbage"} {
		if rec := e.do("GET", "/api/v1/patients", tok, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: want 401, got %d", tok, rec.Code)
		}
	}
	// A mobile patient session has no facility: staff endpoints are forbidden.
	if rec := e.do("GET", "/api/v1/patients", patient, ""); rec.Code != http.StatusForbidden {
		t.Errorf("patient session: want 403, got %d", rec.Code)
	}
	// Another facility cannot see, edit or delete it, and cannot tell it exists.
	// DELETE uses an admin of B: a doctor is rightly 403 on archive regardless of tenant.
	adminB := e.tokenFor(t, "admin-b@example.com", facB, authdomain.RoleAdmin)
	for _, c := range []struct{ method, token, body string }{
		{"GET", docB, ""}, {"PUT", docB, createBody}, {"DELETE", adminB, ""},
	} {
		rec := e.do(c.method, "/api/v1/patients/"+created.ID, c.token, strings.Replace(c.body, `, "consent": true`, "", 1))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s across facilities: want 404, got %d %s", c.method, rec.Code, rec.Body)
		}
	}
	var page struct{ Data []any }
	decode(t, e.do("GET", "/api/v1/patients", docB, ""), &page)
	if len(page.Data) != 0 {
		t.Errorf("facility B must not list A's patients: %v", page.Data)
	}
}

func TestRequestErrors(t *testing.T) {
	e := newEnv(t)
	tok := e.tokenFor(t, "dokter@example.com", facA, authdomain.RoleDoctor)
	e.do("POST", "/api/v1/patients", tok, createBody)

	tests := []struct {
		name, body string
		status     int
		code       string
	}{
		{"duplicate nik", createBody, http.StatusConflict, "nik_taken"},
		{"validation", `{"full_name":"A","sex":"?","birth_date":"x","phone":"1","payment_type":"?","consent":true}`, http.StatusUnprocessableEntity, "validation_failed"},
		{"no consent", strings.Replace(createBody, `"consent": true`, `"consent": false`, 1), http.StatusUnprocessableEntity, "validation_failed"},
		{"unknown field (mass assignment)", `{"full_name":"Budi Baru","sex":"M","birth_date":"1990-01-01","phone":"081234567890","payment_type":"private","consent":true,"facility_id":"` + facB + `","mrn":"RM-999999"}`, http.StatusBadRequest, "invalid_request"},
		{"malformed json", `{"full_name":`, http.StatusBadRequest, "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do("POST", "/api/v1/patients", tok, tt.body)
			if rec.Code != tt.status || errCode(t, rec) != tt.code {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body, tt.status, tt.code)
			}
		})
	}

	// consent is only accepted on create.
	var created struct{ ID string }
	decode(t, e.do("GET", "/api/v1/patients", tok, ""), &struct{ Data []any }{})
	rec := e.do("GET", "/api/v1/patients", tok, "")
	var page struct{ Data []struct{ ID string } }
	decode(t, rec, &page)
	created.ID = page.Data[0].ID
	if rec := e.do("PUT", "/api/v1/patients/"+created.ID, tok, createBody); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT with consent field: want 400, got %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/api/v1/patients/not-a-uuid", tok, ""); rec.Code != http.StatusNotFound {
		t.Errorf("bad id: want 404, got %d", rec.Code)
	}
}

func TestAuditTrailFromHTTP(t *testing.T) {
	e := newEnv(t)
	tok := e.tokenFor(t, "dokter@example.com", facA, authdomain.RoleDoctor)
	var created struct{ ID string }
	decode(t, e.do("POST", "/api/v1/patients", tok, createBody), &created)
	e.do("GET", "/api/v1/patients/"+created.ID, tok, "")
	e.do("GET", "/api/v1/patients?q=Siti", tok, "")

	want := "[patient.create patient.read patient.list]"
	if got := fmt.Sprint(e.audit.Actions()); got != want {
		t.Fatalf("audit = %s, want %s", got, want)
	}
	for _, en := range e.audit.Entries {
		if strings.Contains(fmt.Sprint(en), "Siti") {
			t.Fatalf("audit entry leaks PII: %+v", en)
		}
	}
}
