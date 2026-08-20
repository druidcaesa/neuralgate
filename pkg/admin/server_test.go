package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

func TestHealthz(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAPIPing(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/ping", nil)
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCORSMiddleware(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
}
