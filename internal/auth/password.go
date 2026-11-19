package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime      uint32 = 2
	argonMemory    uint32 = 19 * 1024
	argonThreads   uint8  = 1
	argonKeyLength uint32 = 32
	saltLength            = 16
)

var (
	ErrEmptyPassword       = errors.New("password cannot be empty")
	ErrInvalidPasswordHash = errors.New("invalid password hash")
)

// HashPassword generates an Argon2id hash for a password.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", ErrEmptyPassword
	}

	// Generate a cryptographically secure random salt.
	salt := make([]byte, saltLength)

	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	// Generate the password hash.
	hash := argon2.IDKey(
		[]byte(password),
		salt,
		argonTime,
		argonMemory,
		argonThreads,
		argonKeyLength,
	)

	// Encode salt and hash for database storage.
	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedHash := base64.RawStdEncoding.EncodeToString(hash)

	// Store the algorithm and parameters alongside the hash.
	encoded := fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonTime,
		argonThreads,
		encodedSalt,
		encodedHash,
	)

	return encoded, nil
}

// VerifyPassword checks whether a password matches a stored hash.
func VerifyPassword(password, encodedHash string) (bool, error) {
	if password == "" {
		return false, ErrEmptyPassword
	}

	parts := strings.Split(encodedHash, "$")

	if len(parts) != 6 ||
		parts[0] != "" ||
		parts[1] != "argon2id" ||
		parts[2] != "v=19" ||
		parts[3] != fmt.Sprintf(
			"m=%d,t=%d,p=%d",
			argonMemory,
			argonTime,
			argonThreads,
		) {
		return false, ErrInvalidPasswordHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != saltLength {
		return false, ErrInvalidPasswordHash
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expectedHash) != int(argonKeyLength) {
		return false, ErrInvalidPasswordHash
	}

	// Hash the supplied password using the original salt.
	actualHash := argon2.IDKey(
		[]byte(password),
		salt,
		argonTime,
		argonMemory,
		argonThreads,
		argonKeyLength,
	)

	// Compare hashes in constant time.
	match := subtle.ConstantTimeCompare(expectedHash, actualHash)

	return match == 1, nil
}
