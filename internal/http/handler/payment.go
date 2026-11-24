package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"staynest/internal/http/middleware"
	"staynest/internal/payment"
)

type PaymentService interface {
	Initiate(
		ctx context.Context,
		input payment.InitiateInput,
	) (*payment.CheckoutDetails, error)

	Verify(
		ctx context.Context,
		input payment.VerifyInput,
	) (*payment.Payment, error)
}

type initiatePaymentResponse struct {
	PaymentID       string `json:"payment_id"`
	ProviderOrderID string `json:"provider_order_id"`
	AmountPaise     int64  `json:"amount_paise"`
	Currency        string `json:"currency"`
	KeyID           string `json:"key_id"`
}

type verifyPaymentRequest struct {
	PaymentID         string `json:"payment_id" binding:"required"`
	ProviderOrderID   string `json:"provider_order_id" binding:"required"`
	ProviderPaymentID string `json:"provider_payment_id" binding:"required"`
	Signature         string `json:"signature" binding:"required"`
}

type verifyPaymentResponse struct {
	PaymentID         string `json:"payment_id"`
	BookingID         string `json:"booking_id"`
	Status            string `json:"status"`
	FulfillmentStatus string `json:"fulfillment_status"`
}

// InitiatePayment creates or retrieves a payment and prepares
// its Razorpay checkout details for the authenticated customer.
func InitiatePayment(service PaymentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, exists := middleware.CustomerIDFromContext(c)
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		bookingID := c.Param("id")
		idempotencyKey := strings.TrimSpace(
			c.GetHeader("Idempotency-Key"),
		)

		if idempotencyKey == "" || len(idempotencyKey) > 128 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "a valid Idempotency-Key header is required (maximum 128 characters)",
			})
			return
		}

		checkout, err := service.Initiate(
			c.Request.Context(),
			payment.InitiateInput{
				BookingID:      bookingID,
				CustomerID:     customerID,
				IdempotencyKey: idempotencyKey,
			},
		)

		if err != nil {
			switch {
			case errors.Is(err, payment.ErrInvalidCustomerID):
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "authentication required",
				})

			case errors.Is(err, payment.ErrInvalidBookingID),
				errors.Is(err, payment.ErrInvalidIdempotencyKey):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "invalid payment initiation request",
				})

			case errors.Is(err, payment.ErrBookingNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "booking not found",
				})

			case errors.Is(err, payment.ErrBookingNotPayable):
				c.JSON(http.StatusConflict, gin.H{
					"error": "booking cannot be paid",
				})

			case errors.Is(err, payment.ErrPaymentAlreadyExists),
				errors.Is(err, payment.ErrIdempotencyKeyConflict),
				errors.Is(err, payment.ErrOrderCreationInProgress),
				errors.Is(err, payment.ErrProviderOrderAlreadyExists):
				c.JSON(http.StatusConflict, gin.H{
					"error": "payment initiation conflict",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "unable to initiate payment",
				})
			}

			return
		}

		if checkout == nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "unable to initiate payment",
			})
			return
		}

		c.JSON(http.StatusOK, initiatePaymentResponse{
			PaymentID:       checkout.PaymentID,
			ProviderOrderID: checkout.ProviderOrderID,
			AmountPaise:     checkout.AmountPaise,
			Currency:        checkout.Currency,
			KeyID:           checkout.KeyID,
		})
	}
}

func VerifyPayment(service PaymentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, exists := middleware.CustomerIDFromContext(c)
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		var request verifyPaymentRequest

		if err := decodeJSON(c.Writer, c.Request, &request); err != nil {
			var maxBytesError *http.MaxBytesError

			if errors.As(err, &maxBytesError) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": "request body too large",
				})
				return
			}

			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid payment verification request",
			})
			return
		}

		request.PaymentID = strings.TrimSpace(request.PaymentID)
		request.ProviderOrderID = strings.TrimSpace(request.ProviderOrderID)
		request.ProviderPaymentID = strings.TrimSpace(request.ProviderPaymentID)
		request.Signature = strings.TrimSpace(request.Signature)

		if request.PaymentID == "" ||
			request.ProviderOrderID == "" ||
			request.ProviderPaymentID == "" ||
			request.Signature == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "all payment verification fields are required",
			})
			return
		}

		result, err := service.Verify(
			c.Request.Context(),
			payment.VerifyInput{
				PaymentID:         request.PaymentID,
				CustomerID:        customerID,
				ProviderOrderID:   request.ProviderOrderID,
				ProviderPaymentID: request.ProviderPaymentID,
				Signature:         request.Signature,
			},
		)

		if err != nil {
			switch {
			case errors.Is(err, payment.ErrInvalidCustomerID):
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "authentication required",
				})

			case errors.Is(err, payment.ErrInvalidProviderOrderID),
				errors.Is(err, payment.ErrInvalidProviderPaymentID),
				errors.Is(err, payment.ErrInvalidPaymentSignature):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "invalid payment verification request",
				})

			case errors.Is(err, payment.ErrPaymentNotFound),
				errors.Is(err, payment.ErrPaymentOwnershipMismatch):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "payment not found",
				})

			case errors.Is(err, payment.ErrProviderOrderMismatch),
				errors.Is(err, payment.ErrPaymentAmountMismatch),
				errors.Is(err, payment.ErrPaymentCurrencyMismatch),
				errors.Is(err, payment.ErrPaymentNotCaptured):
				c.JSON(http.StatusConflict, gin.H{
					"error": "payment verification failed",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "unable to verify payment",
				})
			}

			return
		}

		if result == nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "unable to verify payment",
			})
			return
		}

		c.JSON(http.StatusOK, verifyPaymentResponse{
			PaymentID:         result.ID,
			BookingID:         result.BookingID,
			Status:            result.Status,
			FulfillmentStatus: result.FulfillmentStatus,
		})
	}
}
