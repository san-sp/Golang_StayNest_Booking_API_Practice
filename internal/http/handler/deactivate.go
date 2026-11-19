package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"staynest/internal/customer"
	"staynest/internal/http/middleware"
)

type CustomerAccountDeactivator interface {
	Deactivate(
		ctx context.Context,
		id string,
		currentPassword string,
	) error
}

type deactivateCustomerRequest struct {
	CurrentPassword string `json:"current_password"`
}

// DeactivateCustomer handles customer account deactivation.
func DeactivateCustomer(
	service CustomerAccountDeactivator,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, ok := middleware.CustomerIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
			})
			return
		}

		var req deactivateCustomerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}

		err := service.Deactivate(
			c.Request.Context(),
			customerID,
			req.CurrentPassword,
		)

		if err != nil {
			switch {
			case errors.Is(err, customer.ErrInvalidCurrentPassword):
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "current password is incorrect",
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
			"message": "account deactivated successfully",
		})
	}
}
