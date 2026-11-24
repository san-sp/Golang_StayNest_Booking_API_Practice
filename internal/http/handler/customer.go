package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"staynest/internal/customer"
)

type CustomerRegistrar interface {
	Register(
		ctx context.Context,
		name string,
		email string,
		password string,
	) (*customer.Customer, error)
}

type CustomerService interface {
	CustomerRegistrar
	CustomerAuthenticator
	CustomerProfileReader
	CustomerProfileUpdater
	CustomerPasswordChanger
	CustomerAccountDeactivator
}

type registerCustomerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type customerResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type registerCustomerResponse struct {
	Message  string           `json:"message"`
	Customer customerResponse `json:"customer"`
}

// RegisterCustomer handles customer registration.
func RegisterCustomer(service CustomerRegistrar) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerCustomerRequest

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

		created, err := service.Register(
			c.Request.Context(),
			req.Name,
			req.Email,
			req.Password,
		)
		if err != nil {
			switch {
			case errors.Is(err, customer.ErrInvalidName),
				errors.Is(err, customer.ErrInvalidEmail),
				errors.Is(err, customer.ErrPasswordTooShort):

				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			case errors.Is(err, customer.ErrEmailAlreadyExists):
				c.JSON(http.StatusConflict, gin.H{
					"error": "email address already registered",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusCreated, registerCustomerResponse{
			Message: "customer registered successfully",
			Customer: customerResponse{
				ID:    created.ID,
				Name:  created.Name,
				Email: created.Email,
			},
		})
	}
}

type loginCustomerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginCustomerResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type CustomerAuthenticator interface {
	Login(
		ctx context.Context,
		email string,
		password string,
	) (string, error)
}

// LoginCustomer handles customer login.
func LoginCustomer(service CustomerAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginCustomerRequest

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

		accessToken, err := service.Login(
			c.Request.Context(),
			req.Email,
			req.Password,
		)
		if err != nil {
			if errors.Is(err, customer.ErrInvalidCredentials) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "invalid email or password",
				})
				return
			}

			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal server error",
			})
			return
		}

		c.JSON(http.StatusOK, loginCustomerResponse{
			AccessToken: accessToken,
			TokenType:   "Bearer",
			ExpiresIn:   900,
		})
	}
}
