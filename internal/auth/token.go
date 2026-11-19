package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const tokenIssuer = "staynest"

var ErrInvalidToken = errors.New("invalid access token")

type TokenManager struct {
	secret []byte
	ttl    time.Duration
}

type AccessClaims struct {
	jwt.RegisteredClaims
}

func NewTokenManager(secret string, ttl time.Duration) (*TokenManager, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT secret must contain at least 32 bytes")
	}

	if ttl <= 0 {
		return nil, errors.New("JWT token lifetime must be positive")
	}

	return &TokenManager{
		secret: []byte(secret),
		ttl:    ttl,
	}, nil
}

// GenerateAccessToken creates a signed JWT for a customer.
func (m *TokenManager) GenerateAccessToken(customerID string) (string, error) {
	if customerID == "" {
		return "", errors.New("customer ID is required")
	}

	now := time.Now()

	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   customerID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	signedToken, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}

	return signedToken, nil
}

// VerifyAccessToken validates a JWT and returns its customer ID.
func (m *TokenManager) VerifyAccessToken(tokenString string) (string, error) {
	claims := &AccessClaims{}

	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, ErrInvalidToken
			}

			return m.secret, nil
		},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
	)

	if err != nil || token == nil || !token.Valid {
		return "", ErrInvalidToken
	}

	if claims.Subject == "" {
		return "", ErrInvalidToken
	}

	return claims.Subject, nil
}
