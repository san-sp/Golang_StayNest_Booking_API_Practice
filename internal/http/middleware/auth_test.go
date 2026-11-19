package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"staynest/internal/auth"
)

const middlewareTestSecret = "staynest-middleware-test-secret-at-least-32-bytes"

func newMiddlewareTestTokenManager(t *testing.T) *auth.TokenManager {
	t.Helper()

	manager, err := auth.NewTokenManager(
		middlewareTestSecret,
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	return manager
}

type fakeActiveCustomerChecker struct {
	active bool
	err    error
	called bool
	gotID  string
}

func (f *fakeActiveCustomerChecker) IsActive(
	ctx context.Context,
	id string,
) (bool, error) {
	f.called = true
	f.gotID = id

	return f.active, f.err
}

func TestAuthenticateAllowsValidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := newMiddlewareTestTokenManager(t)

	token, err := manager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	router := gin.New()

	router.GET(
		"/protected",
		Authenticate(manager, &fakeActiveCustomerChecker{
			active: true,
		}),
		func(c *gin.Context) {
			customerID, exists := CustomerIDFromContext(c)
			if !exists {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "customer ID missing",
				})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"customer_id": customerID,
			})
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s",
			rec.Code, rec.Body.String())
	}

	expected := `{"customer_id":"customer-123"}`
	if rec.Body.String() != expected {
		t.Fatalf("expected %s, got %s", expected, rec.Body.String())
	}
}

func TestAuthenticateRejectsMissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := newMiddlewareTestTokenManager(t)
	checker := &fakeActiveCustomerChecker{active: true}

	router := gin.New()
	router.GET("/protected", Authenticate(manager, checker), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
}

func TestAuthenticateRejectsMalformedAuthorizationHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := newMiddlewareTestTokenManager(t)
	checker := &fakeActiveCustomerChecker{active: true}

	tests := []struct {
		name   string
		header string
	}{
		{
			name:   "missing bearer scheme",
			header: "abc123",
		},
		{
			name:   "missing token",
			header: "Bearer",
		},
		{
			name:   "extra fields",
			header: "Bearer abc123 extra",
		},
		{
			name:   "wrong scheme",
			header: "Basic abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/protected", Authenticate(manager, checker), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", tt.header)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected status 401, got %d", rec.Code)
			}
		})
	}
}

func TestAuthenticateRejectsInvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := newMiddlewareTestTokenManager(t)
	checker := &fakeActiveCustomerChecker{active: true}

	router := gin.New()
	router.GET("/protected", Authenticate(manager, checker), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.value")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
}

func TestAuthenticateRejectsInactiveCustomer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := newMiddlewareTestTokenManager(t)

	token, err := manager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatal(err)
	}

	checker := &fakeActiveCustomerChecker{active: false}

	router := gin.New()
	router.GET("/protected", Authenticate(manager, checker), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s", rec.Code, rec.Body.String())
	}

	if !checker.called {
		t.Fatal("expected active customer checker to be called")
	}

	if checker.gotID != "customer-123" {
		t.Fatalf("expected customer-123, got %q", checker.gotID)
	}

	if rec.Body.String() != "{\"error\":\"account is inactive\"}" {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}

func TestAuthenticateReturnsServiceUnavailableOnDatabaseError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := newMiddlewareTestTokenManager(t)

	token, err := manager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatal(err)
	}

	checker := &fakeActiveCustomerChecker{
		err: errors.New("database unavailable"),
	}

	router := gin.New()
	router.GET("/protected", Authenticate(manager, checker), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"expected status 503, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	if !checker.called {
		t.Fatal("expected active customer checker to be called")
	}

	if strings.Contains(rec.Body.String(), "database unavailable") {
		t.Fatal("internal database error must not be exposed")
	}
}

type fakeAdminChecker struct {
	isAdmin bool
	err     error
	called  bool
	gotID   string
}

func (f *fakeAdminChecker) IsAdmin(
	ctx context.Context,
	id string,
) (bool, error) {
	f.called = true
	f.gotID = id

	return f.isAdmin, f.err
}

func TestRequireAdminAllowsAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)

	checker := &fakeAdminChecker{isAdmin: true}

	router := gin.New()
	router.GET("/admin", func(c *gin.Context) {
		c.Set(customerIDContextKey, "admin-123")
		c.Next()
	}, RequireAdmin(checker), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !checker.called {
		t.Fatal("expected admin checker to be called")
	}

	if checker.gotID != "admin-123" {
		t.Fatalf("expected admin-123, got %q", checker.gotID)
	}
}

func TestRequireAdminRejectsCustomer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	checker := &fakeAdminChecker{isAdmin: false}

	router := gin.New()
	router.GET("/admin", func(c *gin.Context) {
		c.Set(customerIDContextKey, "customer-123")
		c.Next()
	}, RequireAdmin(checker), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequireAdminRejectsMissingCustomerID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	checker := &fakeAdminChecker{isAdmin: true}

	router := gin.New()
	router.GET("/admin", RequireAdmin(checker), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}

	if checker.called {
		t.Fatal("admin checker should not be called without customer ID")
	}
}

func TestRequireAdminReturnsServiceUnavailableOnDatabaseError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	checker := &fakeAdminChecker{
		err: errors.New("database unavailable"),
	}

	router := gin.New()
	router.GET("/admin", func(c *gin.Context) {
		c.Set(customerIDContextKey, "admin-123")
		c.Next()
	}, RequireAdmin(checker), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}

	if strings.Contains(rec.Body.String(), "database unavailable") {
		t.Fatal("internal database error must not be exposed")
	}
}
