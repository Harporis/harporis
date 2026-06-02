package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz_NATSConnected(t *testing.T) {
	h := New()
	h.SetNATSConnected(true)
	rr := httptest.NewRecorder()
	h.HealthzHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("healthz with NATS up: code = %d, want 200", rr.Code)
	}
}

func TestHealthz_NATSDown(t *testing.T) {
	h := New()
	h.SetNATSConnected(false)
	rr := httptest.NewRecorder()
	h.HealthzHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("healthz with NATS down: code = %d, want 503", rr.Code)
	}
}

func TestReadyz_AllReady(t *testing.T) {
	h := New()
	h.SetNATSConnected(true)
	h.SetConsumerCreated(true)
	h.SetWorkerStarted(true)
	rr := httptest.NewRecorder()
	h.ReadyzHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("readyz when ready: code = %d, want 200", rr.Code)
	}
}

func TestReadyz_NotReadyIfAnyFalse(t *testing.T) {
	h := New()
	h.SetNATSConnected(true)
	h.SetConsumerCreated(true)
	h.SetWorkerStarted(false)
	rr := httptest.NewRecorder()
	h.ReadyzHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz with worker not started: code = %d, want 503", rr.Code)
	}
}
