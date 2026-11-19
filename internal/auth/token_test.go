package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testJWTSecret = "staynest-test-secret-at-least-32-bytes-long"

func TestGenerateAndVerifyAccessToken(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	customerID := "customer-123"

	token, err := manager.GenerateAccessToken(customerID)
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	if token == "" {
		t.Fatal("expected a non-empty token")
	}

	gotID, err := manager.VerifyAccessToken(token)
	if err != nil {
		t.Fatalf("VerifyAccessToken() error = %v", err)
	}

	if gotID != customerID {
		t.Fatalf("expected customer ID %q, got %q", customerID, gotID)
	}
}

func TestVerifyAccessTokenRejectsTamperedToken(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	token, err := manager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	tamperedToken := token + "tampered"

	_, err = manager.VerifyAccessToken(tamperedToken)

	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestVerifyAccessTokenRejectsWrongSecret(t *testing.T) {
	manager1, err := NewTokenManager(testJWTSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	manager2, err := NewTokenManager(
		"another-test-secret-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	token, err := manager1.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	_, err = manager2.VerifyAccessToken(token)

	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestVerifyAccessTokenRejectsExpiredToken(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   "customer-123",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	expiredToken, err := token.SignedString(manager.secret)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}

	_, err = manager.VerifyAccessToken(expiredToken)

	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestNewTokenManagerRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		ttl    time.Duration
	}{
		{
			name:   "short secret",
			secret: "short",
			ttl:    15 * time.Minute,
		},
		{
			name:   "zero lifetime",
			secret: testJWTSecret,
			ttl:    0,
		},
		{
			name:   "negative lifetime",
			secret: testJWTSecret,
			ttl:    -time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTokenManager(tt.secret, tt.ttl)

			if err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestGenerateAccessTokenRejectsEmptyCustomerID(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	_, err = manager.GenerateAccessToken("")

	if err == nil {
		t.Fatal("expected error for empty customer ID")
	}
}
