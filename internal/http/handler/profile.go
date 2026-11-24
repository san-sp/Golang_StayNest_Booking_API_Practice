package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"staynest/internal/customer"
	"staynest/internal/http/middleware"
)

type CustomerProfileReader interface {
	GetByID(ctx context.Context, id string) (*customer.Customer, error)
}

type CustomerProfileUpdater interface {
	UpdateName(
		ctx context.Context,
		id string,
		name string,
	) (*customer.Customer, error)
}

type customerProfileResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// GetCustomerProfile returns the authenticated customer's profile.
func GetCustomerProfile(service CustomerProfileReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, exists := middleware.CustomerIDFromContext(c)
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		profile, err := service.GetByID(
			c.Request.Context(),
			customerID,
		)
		if err != nil {
			if errors.Is(err, customer.ErrCustomerNotFound) {
				c.JSON(http.StatusNotFound, gin.H{
					"error": "customer not found",
				})
				return
			}

			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal server error",
			})
			return
		}

		c.JSON(http.StatusOK, customerProfileResponse{
			ID:    profile.ID,
			Name:  profile.Name,
			Email: profile.Email,
		})
	}
}

type updateCustomerProfileRequest struct {
	Name string `json:"name"`
}

// UpdateCustomerProfile updates the authenticated customer's profile.
func UpdateCustomerProfile(service CustomerProfileUpdater) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, exists := middleware.CustomerIDFromContext(c)
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		var req updateCustomerProfileRequest

		if err := decodeJSON(c.Writer, c.Request, &req); err != nil {
			var maxBytesError *http.MaxBytesError

			if errors.As(err, &maxBytesError) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": "request body too large",
				})
				return
			}

			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}

		updated, err := service.UpdateName(
			c.Request.Context(),
			customerID,
			req.Name,
		)

		if err != nil {
			switch {
			case errors.Is(err, customer.ErrInvalidName),
				errors.Is(err, customer.ErrInvalidNameLength):

				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
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

		c.JSON(http.StatusOK, customerProfileResponse{
			ID:    updated.ID,
			Name:  updated.Name,
			Email: updated.Email,
		})
	}
}
