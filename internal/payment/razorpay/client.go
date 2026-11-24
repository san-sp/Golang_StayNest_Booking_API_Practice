package razorpay

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.razorpay.com/v1"

type Client struct {
	keyID      string
	keySecret  string
	baseURL    string
	httpClient *http.Client
}

func NewClient(
	keyID string,
	keySecret string,
	httpClient *http.Client,
) (*Client, error) {
	return newClient(keyID, keySecret, defaultBaseURL, httpClient)
}

func newClient(
	keyID string,
	keySecret string,
	baseURL string,
	httpClient *http.Client,
) (*Client, error) {
	keyID = strings.TrimSpace(keyID)
	keySecret = strings.TrimSpace(keySecret)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")

	if keyID == "" {
		return nil, errors.New("Razorpay key ID is required")
	}

	if keySecret == "" {
		return nil, errors.New("Razorpay key secret is required")
	}

	if baseURL == "" {
		return nil, errors.New("Razorpay base URL is required")
	}

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 15 * time.Second,
		}
	}

	return &Client{
		keyID:      keyID,
		keySecret:  keySecret,
		baseURL:    baseURL,
		httpClient: httpClient,
	}, nil
}
