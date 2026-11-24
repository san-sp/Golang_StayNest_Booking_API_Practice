package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"staynest/internal/customer"
)

type fakeCustomerProfileReader struct {
	profile *customer.Customer
	err     error
	gotID   string
}

func (f *fakeCustomerProfileReader) GetByID(
	ctx context.Context,
	id string,
) (*customer.Customer, error) {
	f.gotID = id
	return f.profile, f.err
}

type fakeCustomerProfileUpdater struct {
	updated *customer.Customer
	err     error
	gotID   string
	gotName string
}

func (f *fakeCustomerProfileUpdater) UpdateName(
	ctx context.Context,
	id string,
	name string,
) (*customer.Customer, error) {
	f.gotID = id
	f.gotName = name
	return f.updated, f.err
}

func TestGetCustomerProfileSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerProfileReader{
		profile: &customer.Customer{
			ID:           "customer-123",
			Name:         "Alex Smith",
			Email:        "alex@example.com",
			PasswordHash: "secret-password-hash",
		},
	}

	router := gin.New()

	router.GET(
		"/api/v1/customers/me",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		GetCustomerProfile(service),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/customers/me",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s",
			rec.Code, rec.Body.String())
	}

	expected := `{"id":"customer-123","name":"Alex Smith","email":"alex@example.com"}`
	if strings.TrimSpace(rec.Body.String()) != expected {
		t.Fatalf("expected %s, got %s", expected, rec.Body.String())
	}

	if service.gotID != "customer-123" {
		t.Fatalf("expected customer ID customer-123, got %q", service.gotID)
	}

	if strings.Contains(rec.Body.String(), "secret-password-hash") {
		t.Fatal("profile response must not expose password hash")
	}
}

func TestGetCustomerProfileRejectsMissingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerProfileReader{}

	router := gin.New()
	router.GET("/api/v1/customers/me", GetCustomerProfile(service))

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/customers/me",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}

	if service.gotID != "" {
		t.Fatal("service should not be called without an authenticated identity")
	}
}

func TestGetCustomerProfileNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerProfileReader{
		err: customer.ErrCustomerNotFound,
	}

	router := gin.New()
	router.GET(
		"/api/v1/customers/me",
		func(c *gin.Context) {
			c.Set("customer_id", "deleted-customer")
			c.Next()
		},
		GetCustomerProfile(service),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/customers/me",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}

func TestGetCustomerProfileHidesInternalErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerProfileReader{
		err: errors.New("database connection failed"),
	}

	router := gin.New()
	router.GET(
		"/api/v1/customers/me",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		GetCustomerProfile(service),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/customers/me",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "database connection failed") {
		t.Fatal("internal error details must not be exposed")
	}
}

func TestUpdateCustomerProfileSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerProfileUpdater{
		updated: &customer.Customer{
			ID:    "customer-123",
			Name:  "Alex Johnson",
			Email: "alex@example.com",
		},
	}

	router := gin.New()
	router.PATCH(
		"/api/v1/customers/me",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		UpdateCustomerProfile(service),
	)

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/customers/me",
		strings.NewReader(`{"name":"Alex Johnson"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	expected := `{"id":"customer-123","name":"Alex Johnson","email":"alex@example.com"}`
	if strings.TrimSpace(rec.Body.String()) != expected {
		t.Fatalf("expected %s, got %s", expected, rec.Body.String())
	}

	if service.gotID != "customer-123" {
		t.Errorf("expected customer ID customer-123, got %q", service.gotID)
	}

	if service.gotName != "Alex Johnson" {
		t.Errorf("expected name Alex Johnson, got %q", service.gotName)
	}
}

func TestUpdateCustomerProfileRejectsMissingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerProfileUpdater{}

	router := gin.New()
	router.PATCH(
		"/api/v1/customers/me",
		UpdateCustomerProfile(service),
	)

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/customers/me",
		strings.NewReader(`{"name":"Alex Johnson"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}

	if service.gotID != "" {
		t.Fatal("service must not be called without authenticated identity")
	}
}
func TestUpdateCustomerProfileRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerProfileUpdater{}

	router := gin.New()
	router.PATCH(
		"/api/v1/customers/me",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		UpdateCustomerProfile(service),
	)

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/customers/me",
		strings.NewReader(`{"name":`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	if service.gotID != "" {
		t.Fatal("service must not be called for invalid JSON")
	}
}

func TestUpdateCustomerProfileHandlesServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "empty name",
			serviceErr: customer.ErrInvalidName,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "name too long",
			serviceErr: customer.ErrInvalidNameLength,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "customer not found",
			serviceErr: customer.ErrCustomerNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "database error",
			serviceErr: errors.New("database connection failed"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)

			service := &fakeCustomerProfileUpdater{
				err: tt.serviceErr,
			}

			router := gin.New()
			router.PATCH(
				"/api/v1/customers/me",
				func(c *gin.Context) {
					c.Set("customer_id", "customer-123")
					c.Next()
				},
				UpdateCustomerProfile(service),
			)

			req := httptest.NewRequest(
				http.MethodPatch,
				"/api/v1/customers/me",
				strings.NewReader(`{"name":"Alex Johnson"}`),
			)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d: %s",
					tt.wantStatus,
					rec.Code,
					rec.Body.String(),
				)
			}

			if strings.Contains(rec.Body.String(), "database connection failed") {
				t.Fatal("internal error details must not be exposed")
			}
		})
	}
}

func TestUpdateCustomerProfileRejectsUnknownFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerProfileUpdater{}

	router := gin.New()
	router.PATCH(
		"/api/v1/customers/me",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		UpdateCustomerProfile(service),
	)

	body := `{
		"name": "Alex Johnson",
		"customer_id": "attacker-customer"
	}`

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/customers/me",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.gotID != "" {
		t.Fatal("profile service must not be called for unknown fields")
	}
}

func TestUpdateCustomerProfileRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerProfileUpdater{}

	router := gin.New()
	router.PATCH(
		"/api/v1/customers/me",
		func(c *gin.Context) {
			c.Set("customer_id", "customer-123")
			c.Next()
		},
		UpdateCustomerProfile(service),
	)

	body := `{"name":"` +
		strings.Repeat("a", maxJSONBodySize)

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/customers/me",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.gotID != "" {
		t.Fatal("profile service must not be called for oversized bodies")
	}
}
