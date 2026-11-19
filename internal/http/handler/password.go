package handler

import (
	"context"
	"errors"
	"net/http"

	"staynest/internal/customer"
	"staynest/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

type CustomerPasswordChanger interface {
	ChangePassword(
		ctx context.Context,
		id string,
		currentPassword string,
		newPassword string,
	) error
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func ChangeCustomerPassword(
	service CustomerPasswordChanger,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, ok := middleware.CustomerIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
			})
			return
		}

		var req changePasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}

		err := service.ChangePassword(
			c.Request.Context(),
			customerID,
			req.CurrentPassword,
			req.NewPassword,
		)

		if err != nil {
			switch {
			case errors.Is(err, customer.ErrInvalidCurrentPassword):
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "current password is incorrect",
				})

			case errors.Is(err, customer.ErrPasswordTooShort):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "new password must be at least 8 characters",
				})

			case errors.Is(err, customer.ErrCustomerNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "customer not found",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message": "password changed successfully",
		})
	}
}
