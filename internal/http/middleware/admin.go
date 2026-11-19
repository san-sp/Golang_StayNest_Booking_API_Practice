package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminChecker checks whether an active customer has administrator privileges.
type AdminChecker interface {
	IsAdmin(ctx context.Context, id string) (bool, error)
}

// RequireAdmin ensures that the authenticated customer is an administrator.
func RequireAdmin(adminChecker AdminChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, exists := CustomerIDFromContext(c)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		isAdmin, err := adminChecker.IsAdmin(
			c.Request.Context(),
			customerID,
		)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "unable to verify administrator privileges",
			})
			return
		}

		if !isAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "administrator privileges required",
			})
			return
		}

		c.Next()
	}
}
