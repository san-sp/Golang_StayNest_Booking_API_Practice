package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type AccessTokenVerifier interface {
	VerifyAccessToken(tokenString string) (string, error)
}

type ActiveCustomerChecker interface {
	IsActive(ctx context.Context, id string) (bool, error)
}

const customerIDContextKey = "customer_id"

// Authenticate verifies the Bearer token and checks that the customer is active.
func Authenticate(
	verifier AccessTokenVerifier,
	activeCustomerChecker ActiveCustomerChecker,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")

		parts := strings.Fields(header)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		customerID, err := verifier.VerifyAccessToken(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid or expired access token",
			})
			return
		}

		active, err := activeCustomerChecker.IsActive(
			c.Request.Context(),
			customerID,
		)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "unable to verify account status",
			})
			return
		}

		if !active {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "account is inactive",
			})
			return
		}

		c.Set(customerIDContextKey, customerID)
		c.Next()
	}
}

// CustomerIDFromContext retrieves the authenticated customer's ID.
func CustomerIDFromContext(c *gin.Context) (string, bool) {
	customerID, exists := c.Get(customerIDContextKey)
	if !exists {
		return "", false
	}

	id, ok := customerID.(string)
	if !ok || id == "" {
		return "", false
	}

	return id, true
}
