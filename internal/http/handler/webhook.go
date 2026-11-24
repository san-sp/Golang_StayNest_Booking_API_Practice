package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"staynest/internal/payment"
)

const maxWebhookBodySize = 1 << 20 // 1 MiB

type PaymentWebhookService interface {
	Process(
		ctx context.Context,
		eventID string,
		signature string,
		body []byte,
	) error
}

// RazorpayWebhook handles authenticated Razorpay webhook deliveries.
func RazorpayWebhook(service PaymentWebhookService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "webhook service unavailable",
			})
			return
		}

		eventID := strings.TrimSpace(
			c.GetHeader("x-razorpay-event-id"),
		)

		signature := strings.TrimSpace(
			c.GetHeader("X-Razorpay-Signature"),
		)

		if eventID == "" || signature == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "missing webhook headers",
			})
			return
		}

		// Limit the request body before reading it.
		c.Request.Body = http.MaxBytesReader(
			c.Writer,
			c.Request.Body,
			maxWebhookBodySize,
		)

		// Preserve the original bytes for Razorpay HMAC verification.
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			var maxBytesError *http.MaxBytesError

			if errors.As(err, &maxBytesError) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": "webhook request body too large",
				})
				return
			}

			c.JSON(http.StatusBadRequest, gin.H{
				"error": "unable to read webhook request body",
			})
			return
		}

		err = service.Process(
			c.Request.Context(),
			eventID,
			signature,
			body,
		)

		if err != nil {
			switch {
			case errors.Is(err, payment.ErrInvalidWebhookSignature),
				errors.Is(err, payment.ErrInvalidWebhookPayload),
				errors.Is(err, payment.ErrWebhookEventConflict),
				errors.Is(err, payment.ErrPaymentNotCaptured),
				errors.Is(err, payment.ErrProviderOrderMismatch),
				errors.Is(err, payment.ErrPaymentAmountMismatch),
				errors.Is(err, payment.ErrPaymentCurrencyMismatch):

				c.JSON(http.StatusBadRequest, gin.H{
					"error": "invalid webhook notification",
				})

			default:
				// Return 500 for processing failures so Razorpay can retry.
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "unable to process webhook notification",
				})
			}

			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	}
}
