package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeDB struct {
	err error
}

func (f fakeDB) Ping(ctx context.Context) error {
	return f.err
}

func TestHealth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/health", Health)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	expected := `{"status":"ok"}`
	if rec.Body.String() != expected {
		t.Errorf("expected body %s, got %s", expected, rec.Body.String())
	}
}

func TestReadySuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/ready", Ready(fakeDB{}))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	expected := `{"status":"ready"}`
	if rec.Body.String() != expected {
		t.Errorf("expected body %s, got %s", expected, rec.Body.String())
	}
}

func TestReadyDatabaseFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/ready", Ready(fakeDB{
		err: errors.New("database connection failed"),
	}))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusServiceUnavailable,
			rec.Code,
		)
	}

	expected := `{"error":"database unavailable","status":"not ready"}`
	if rec.Body.String() != expected {
		t.Errorf("expected body %s, got %s", expected, rec.Body.String())
	}
}
