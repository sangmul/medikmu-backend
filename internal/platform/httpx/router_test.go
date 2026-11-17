package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sangmul/medikmu-backend/internal/platform/config"
)

func TestHealthz(t *testing.T) {
	h := NewRouter(config.Config{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
