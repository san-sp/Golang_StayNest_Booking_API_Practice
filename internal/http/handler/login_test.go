package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"staynest/internal/customer"
)

type fakeCustomerAuthenticator struct {
	accessToken string
	err         error

	gotEmail    string
	gotPassword string
}

func (f *fakeCustomerAuthenticator) Login(
	ctx context.Context,
	email string,
	password string,
) (string, error) {
	f.gotEmail = email
	f.gotPassword = password

	return f.accessToken, f.err
}

func TestLoginCustomerSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerAuthenticator{
		accessToken: "test-access-token",
	}

	router := gin.New()
	router.POST("/api/v1/auth/login", LoginCustomer(service))

	body := `{
		"email": "alex@example.com",
		"password": "MySecretPassword123"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s",
			http.StatusOK, rec.Code, rec.Body.String())
	}

	var response loginCustomerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response.AccessToken != "test-access-token" {
		t.Errorf("unexpected access token: %q", response.AccessToken)
	}

	if response.TokenType != "Bearer" {
		t.Errorf("expected Bearer token type, got %q", response.TokenType)
	}

	if response.ExpiresIn != 900 {
		t.Errorf("expected expiration of 900 seconds, got %d", response.ExpiresIn)
	}

	if service.gotEmail != "alex@example.com" {
		t.Errorf("unexpected email passed to service: %q", service.gotEmail)
	}

	if service.gotPassword != "MySecretPassword123" {
		t.Fatal("expected password to be passed to service")
	}
}

func TestLoginCustomerRejectsMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerAuthenticator{}
	router := gin.New()
	router.POST("/api/v1/auth/login", LoginCustomer(service))

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(`{"email":`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	if service.gotEmail != "" || service.gotPassword != "" {
		t.Fatal("service should not be called for malformed JSON")
	}
}

func TestLoginCustomerRejectsInvalidCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerAuthenticator{
		err: customer.ErrInvalidCredentials,
	}

	router := gin.New()
	router.POST("/api/v1/auth/login", LoginCustomer(service))

	body := `{
		"email": "alex@example.com",
		"password": "WrongPassword123"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "customer not found") {
		t.Fatal("response must not reveal whether the customer exists")
	}
}

func TestLoginCustomerHidesInternalErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeCustomerAuthenticator{
		err: errors.New("database connection failed"),
	}

	router := gin.New()
	router.POST("/api/v1/auth/login", LoginCustomer(service))

	body := `{
		"email": "alex@example.com",
		"password": "Password123"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "database connection failed") {
		t.Fatal("internal error details must not be exposed")
	}
}
