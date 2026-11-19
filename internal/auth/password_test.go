package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashPasswordAndVerifyPassword(t *testing.T) {
	password := "MySecretPassword123"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if hash == password {
		t.Fatal("password hash must not equal original password")
	}

	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}

	match, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}

	if !match {
		t.Fatal("expected password to match")
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("CorrectPassword123")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	match, err := VerifyPassword("WrongPassword123", hash)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}

	if match {
		t.Fatal("expected wrong password not to match")
	}
}

func TestHashPasswordRejectsEmptyPassword(t *testing.T) {
	_, err := HashPassword("")

	if !errors.Is(err, ErrEmptyPassword) {
		t.Fatalf("expected ErrEmptyPassword, got %v", err)
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	_, err := VerifyPassword("SomePassword123", "invalid-hash")

	if !errors.Is(err, ErrInvalidPasswordHash) {
		t.Fatalf("expected ErrInvalidPasswordHash, got %v", err)
	}
}

func TestVerifyPasswordRejectsEmptyPassword(t *testing.T) {
	_, err := VerifyPassword("", "some-hash")

	if !errors.Is(err, ErrEmptyPassword) {
		t.Fatalf("expected ErrEmptyPassword, got %v", err)
	}
}
