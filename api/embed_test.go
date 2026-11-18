package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sangmul/medikmu-backend/api"
)

func TestDocsServeSpecAndViewer(t *testing.T) {
	mux := http.NewServeMux()
	api.Mount(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "openapi: 3.") {
		t.Fatalf("/openapi.yaml: %d %.60q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/patients/{id}") {
		t.Fatal("embedded spec looks stale: missing /patients/{id}")
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SwaggerUIBundle") {
		t.Fatalf("/docs: %d", rec.Code)
	}
}
