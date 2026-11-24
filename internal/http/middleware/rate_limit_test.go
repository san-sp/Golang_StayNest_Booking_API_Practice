package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestNewIPRateLimiterRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		limit  int
		window time.Duration
	}{
		{
			name:   "zero limit",
			limit:  0,
			window: time.Minute,
		},
		{
			name:   "negative limit",
			limit:  -1,
			window: time.Minute,
		},
		{
			name:   "zero window",
			limit:  10,
			window: 0,
		},
		{
			name:   "negative window",
			limit:  10,
			window: -time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewIPRateLimiter(tt.limit, tt.window)

			if err != ErrInvalidRateLimit {
				t.Fatalf("expected ErrInvalidRateLimit, got %v", err)
			}
		})
	}
}

func TestIPRateLimiterAllowsRequestsWithinLimit(t *testing.T) {
	limiter, err := NewIPRateLimiter(3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		allowed, _ := limiter.allow("192.0.2.1")

		if !allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestIPRateLimiterRejectsRequestsAfterLimit(t *testing.T) {
	limiter, err := NewIPRateLimiter(2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	first, _ := limiter.allow("192.0.2.1")
	second, _ := limiter.allow("192.0.2.1")
	third, retryAfter := limiter.allow("192.0.2.1")

	if !first || !second {
		t.Fatal("first two requests should be allowed")
	}

	if third {
		t.Fatal("third request should be rejected")
	}

	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Fatalf("expected retry after a positive duration up to 1 minute, got %v",
			retryAfter)
	}
}

func TestIPRateLimiterResetsAfterWindowExpires(t *testing.T) {
	limiter, err := NewIPRateLimiter(2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time {
		return now
	}

	limiter.allow("192.0.2.1")
	limiter.allow("192.0.2.1")

	allowed, _ := limiter.allow("192.0.2.1")
	if allowed {
		t.Fatal("request should be rejected before window expires")
	}

	now = now.Add(time.Minute)

	allowed, _ = limiter.allow("192.0.2.1")
	if !allowed {
		t.Fatal("request should be allowed after window expires")
	}
}

func TestIPRateLimiterTracksDifferentIPsIndependently(t *testing.T) {
	limiter, err := NewIPRateLimiter(1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	first, _ := limiter.allow("192.0.2.1")
	second, _ := limiter.allow("192.0.2.2")
	third, _ := limiter.allow("192.0.2.1")

	if !first || !second {
		t.Fatal("first request from each IP should be allowed")
	}

	if third {
		t.Fatal("second request from the same IP should be rejected")
	}
}

func TestIPRateLimiterHandlesConcurrentRequests(t *testing.T) {
	const (
		limit    = 10
		total    = 100
		clientIP = "192.0.2.1"
	)

	limiter, err := NewIPRateLimiter(limit, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	var (
		wg      sync.WaitGroup
		allowed atomic.Int64
	)

	wg.Add(total)

	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()

			ok, _ := limiter.allow(clientIP)
			if ok {
				allowed.Add(1)
			}
		}()
	}

	wg.Wait()

	if got := allowed.Load(); got != limit {
		t.Fatalf("expected exactly %d allowed requests, got %d", limit, got)
	}
}

func TestIPRateLimiterMiddlewareReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter, err := NewIPRateLimiter(2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	fixedNow := time.Date(
		2026, 11, 24, 12, 0, 0, 0, time.UTC,
	)

	limiter.now = func() time.Time {
		return fixedNow
	}

	router := gin.New()

	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}

	router.Use(limiter.Middleware())
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.0.2.1:1234"

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if i < 2 && rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}

		if i == 2 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("expected 429, got %d", rec.Code)
			}

			if rec.Header().Get("Retry-After") != strconv.Itoa(60) {
				t.Errorf("unexpected Retry-After: %q",
					rec.Header().Get("Retry-After"))
			}

			if !strings.Contains(rec.Body.String(), "too many login attempts") {
				t.Errorf("unexpected response body: %s", rec.Body.String())
			}
		}
	}
}

func TestIPRateLimiterIgnoresSpoofedForwardedFor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter, err := NewIPRateLimiter(1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()

	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}

	router.Use(limiter.Middleware())
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	makeRequest := func(forwardedFor string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("X-Forwarded-For", forwardedFor)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		return rec
	}

	first := makeRequest("203.0.113.10")
	if first.Code != http.StatusOK {
		t.Fatalf("expected first request to pass, got %d", first.Code)
	}

	second := makeRequest("203.0.113.20")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected spoofed second request to be rejected, got %d",
			second.Code)
	}
}

func TestIPRateLimiterEvictsLeastRecentlyUsedClientWhenFull(t *testing.T) {
	limiter, err := NewIPRateLimiter(1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// Use a small capacity so we can test eviction easily.
	limiter.maxClients = 2

	// First client enters the cache.
	allowed, _ := limiter.allow("192.0.2.1")
	if !allowed {
		t.Fatal("first client should be allowed")
	}

	// Second client enters the cache.
	allowed, _ = limiter.allow("192.0.2.2")
	if !allowed {
		t.Fatal("second client should be allowed")
	}

	// Access the first client again. It becomes most recently used,
	// even though this request exceeds its limit.
	allowed, _ = limiter.allow("192.0.2.1")
	if allowed {
		t.Fatal("second request from first client should be rejected")
	}

	// Third client arrives. The second client should be evicted.
	allowed, _ = limiter.allow("192.0.2.3")
	if !allowed {
		t.Fatal("third client should be allowed")
	}

	if _, exists := limiter.clients["192.0.2.2"]; exists {
		t.Fatal("least recently used client should have been evicted")
	}

	if len(limiter.clients) != 2 {
		t.Fatalf("expected 2 tracked clients, got %d", len(limiter.clients))
	}

	if limiter.lru.Len() != 2 {
		t.Fatalf("expected LRU list length 2, got %d", limiter.lru.Len())
	}

	// The first client's existing limit should still be enforced.
	allowed, _ = limiter.allow("192.0.2.1")
	if allowed {
		t.Fatal("first client's limit should remain active")
	}

	// The evicted second client is treated as a new client.
	allowed, _ = limiter.allow("192.0.2.2")
	if !allowed {
		t.Fatal("evicted client should be allowed as a new entry")
	}
}
