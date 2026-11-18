package familyhttp_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sangmul/medikmu-backend/internal/family/familytest"
	familyhttp "github.com/sangmul/medikmu-backend/internal/family/http"
	"github.com/sangmul/medikmu-backend/internal/family/service"
	"github.com/sangmul/medikmu-backend/internal/platform/authctx"
	"github.com/sangmul/medikmu-backend/internal/platform/config"
	"github.com/sangmul/medikmu-backend/internal/platform/httpx"
)

func newServer() http.Handler {
	verify := func(tok string) (authctx.Principal, error) {
		if strings.HasPrefix(tok, "tok:") {
			return authctx.Principal{UserID: strings.TrimPrefix(tok, "tok:")}, nil
		}
		return authctx.Principal{}, errors.New("bad token")
	}
	h := familyhttp.New(service.New(familytest.NewMemStore()), verify)
	return httpx.NewRouter(config.Config{}, h.Mount)
}

func do(srv http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
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

const airaBody = `{"full_name":"Aira Putri","relation":"child","sex":"F","birth_date":"2023-05-10","avatar_tone":"teal",
 "bpjs":{"number":"0001234567890","holder_name":"AIRA PUTRI","service_class":2}}`

func TestCRUDFlow(t *testing.T) {
	srv := newServer()
	rec := do(srv, "POST", "/api/v1/family-members", "tok:ana", airaBody)
	if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d %s (cache=%q)", rec.Code, rec.Body, rec.Header().Get("Cache-Control"))
	}
	var m struct {
		ID     string
		HasNIK bool `json:"has_nik"`
		BPJS   struct {
			Number       string
			HolderName   string `json:"holder_name"`
			ServiceClass int    `json:"service_class"`
		}
	}
	decode(t, rec, &m)
	if m.HasNIK || m.BPJS.Number != "0001234567890" || m.BPJS.ServiceClass != 2 || m.BPJS.HolderName != "AIRA PUTRI" {
		t.Fatalf("unexpected body: %s", rec.Body)
	}

	// The mobile app asks for the NIK later, separately; the list then reports it masked.
	rec = do(srv, "PUT", "/api/v1/family-members/"+m.ID+"/nik", "tok:ana", `{"nik":"3174 0123 4567 8901"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"nik_masked":"3174 **** **** 8901"`) || !strings.Contains(rec.Body.String(), `"has_nik":true`) {
		t.Fatalf("set nik: %d %s", rec.Code, rec.Body)
	}
	rec = do(srv, "GET", "/api/v1/family-members", "tok:ana", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Aira Putri") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	rec = do(srv, "PUT", "/api/v1/family-members/"+m.ID, "tok:ana", `{"full_name":"Aira P.","relation":"child","sex":"F","birth_date":"2023-05-10"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"bpjs":{`) {
		t.Fatalf("update replaces fields, so the card is gone: %d %s", rec.Code, rec.Body)
	}
	if rec = do(srv, "DELETE", "/api/v1/family-members/"+m.ID, "tok:ana", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec = do(srv, "GET", "/api/v1/family-members/"+m.ID, "tok:ana", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("after delete: %d", rec.Code)
	}
}

func TestIsolationBetweenAccounts(t *testing.T) {
	srv := newServer()
	var m struct{ ID string }
	decode(t, do(srv, "POST", "/api/v1/family-members", "tok:ana", airaBody), &m)

	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/family-members/" + m.ID, ""},
		{"PUT", "/api/v1/family-members/" + m.ID, airaBody},
		{"PUT", "/api/v1/family-members/" + m.ID + "/nik", `{"nik":"3174012345678901"}`},
		{"DELETE", "/api/v1/family-members/" + m.ID, ""},
	} {
		if rec := do(srv, c.method, c.path, "tok:budi", c.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s as another account: want 404, got %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	if rec := do(srv, "GET", "/api/v1/family-members", "tok:budi", ""); strings.Contains(rec.Body.String(), "Aira") {
		t.Fatalf("budi sees ana's family: %s", rec.Body)
	}
}

func TestRequestErrors(t *testing.T) {
	srv := newServer()
	tests := []struct {
		name, method, path, token, body string
		status                          int
		code                            string
	}{
		{"no token", "GET", "/api/v1/family-members", "", "", 401, "unauthorized"},
		{"validation", "POST", "/api/v1/family-members", "tok:ana", `{"full_name":"A","relation":"x","sex":"?","birth_date":"x"}`, 422, "validation_failed"},
		{"unknown field (mass assignment)", "POST", "/api/v1/family-members", "tok:ana", `{"full_name":"Aira","relation":"child","sex":"F","birth_date":"2023-05-10","user_id":"someone-else"}`, 400, "invalid_request"},
		{"malformed", "POST", "/api/v1/family-members", "tok:ana", `{"full_name":`, 400, "invalid_request"},
		{"bad id", "GET", "/api/v1/family-members/xyz", "tok:ana", "", 404, "not_found"},
		{"bad nik", "PUT", "/api/v1/family-members/00000000-0000-4000-8000-000000000001/nik", "tok:ana", `{"nik":"12"}`, 422, "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(srv, tt.method, tt.path, tt.token, tt.body)
			if rec.Code != tt.status || errCode(t, rec) != tt.code {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body, tt.status, tt.code)
			}
		})
	}
}
