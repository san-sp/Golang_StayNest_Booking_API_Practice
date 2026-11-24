package razorpay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func generateWebhookSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)

	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_123"}}}}`)
	secret := "test-webhook-secret"

	validSignature := generateWebhookSignature(body, secret)

	tests := []struct {
		name      string
		body      []byte
		signature string
		secret    string
		want      bool
	}{
		{
			name:      "valid signature",
			body:      body,
			signature: validSignature,
			secret:    secret,
			want:      true,
		},
		{
			name:      "wrong secret",
			body:      body,
			signature: validSignature,
			secret:    "wrong-secret",
			want:      false,
		},
		{
			name:      "modified body",
			body:      []byte(`{"event":"payment.failed"}`),
			signature: validSignature,
			secret:    secret,
			want:      false,
		},
		{
			name:      "invalid signature",
			body:      body,
			signature: "not-a-valid-signature",
			secret:    secret,
			want:      false,
		},
		{
			name:      "empty signature",
			body:      body,
			signature: "",
			secret:    secret,
			want:      false,
		},
		{
			name:      "empty webhook secret",
			body:      body,
			signature: validSignature,
			secret:    "",
			want:      false,
		},
		{
			name:      "empty body",
			body:      nil,
			signature: validSignature,
			secret:    secret,
			want:      false,
		},
		{
			name:      "signature with surrounding whitespace",
			body:      body,
			signature: "  " + validSignature + "  ",
			secret:    secret,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VerifyWebhookSignature(
				tt.body,
				tt.signature,
				tt.secret,
			)

			if got != tt.want {
				t.Errorf("VerifyWebhookSignature() = %v, want %v",
					got, tt.want)
			}
		})
	}
}
