package razorpay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifyWebhookSignature validates a Razorpay webhook signature
// against the original, unmodified request body.
//
// The webhook secret is separate from the Razorpay API key secret.
func VerifyWebhookSignature(
	body []byte,
	signature string,
	webhookSecret string,
) bool {
	signature = strings.TrimSpace(signature)

	if len(body) == 0 || signature == "" || webhookSecret == "" {
		return false
	}

	providedSignature, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(webhookSecret))
	_, _ = mac.Write(body)

	expectedSignature := mac.Sum(nil)

	return hmac.Equal(providedSignature, expectedSignature)
}
