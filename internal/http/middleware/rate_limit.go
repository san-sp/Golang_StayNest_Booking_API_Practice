package middleware

import (
	"container/list"
	"errors"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	ErrInvalidRateLimit = errors.New("rate limit and window must be positive")
)

const defaultMaxTrackedClients = 10_000

type clientWindow struct {
	startedAt time.Time
	requests  int
}

type clientEntry struct {
	ip     string
	window clientWindow
}

// IPRateLimiter implements a concurrency-safe, in-memory fixed-window
// rate limiter with bounded LRU client tracking.
type IPRateLimiter struct {
	mu         sync.Mutex
	limit      int
	window     time.Duration
	maxClients int

	clients map[string]*list.Element
	lru     *list.List

	now func() time.Time
}

func NewIPRateLimiter(
	limit int,
	window time.Duration,
) (*IPRateLimiter, error) {
	if limit <= 0 || window <= 0 {
		return nil, ErrInvalidRateLimit
	}

	return &IPRateLimiter{
		limit:      limit,
		window:     window,
		maxClients: defaultMaxTrackedClients,
		clients:    make(map[string]*list.Element),
		lru:        list.New(),
		now:        time.Now,
	}, nil
}

// Middleware rejects requests exceeding the configured limit.
func (l *IPRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		if clientIP == "" {
			clientIP = "unknown"
		}

		allowed, retryAfter := l.allow(clientIP)

		if !allowed {
			retrySeconds := int(math.Ceil(retryAfter.Seconds()))
			if retrySeconds < 1 {
				retrySeconds = 1
			}

			c.Header("Retry-After", strconv.Itoa(retrySeconds))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "too many login attempts; try again later",
			})
			return
		}

		c.Next()
	}
}

func (l *IPRateLimiter) allow(clientIP string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	element, exists := l.clients[clientIP]

	if exists {
		entry := element.Value.(*clientEntry)

		// Reset an expired fixed window.
		if !now.Before(entry.window.startedAt.Add(l.window)) {
			entry.window = clientWindow{
				startedAt: now,
				requests:  1,
			}

			l.lru.MoveToFront(element)
			return true, 0
		}

		l.lru.MoveToFront(element)

		if entry.window.requests >= l.limit {
			retryAfter := entry.window.startedAt.Add(l.window).Sub(now)
			return false, retryAfter
		}

		entry.window.requests++
		return true, 0
	}

	// Evict the least recently used client when the cache is full.
	if len(l.clients) >= l.maxClients {
		l.evictLRU()
	}

	entry := &clientEntry{
		ip: clientIP,
		window: clientWindow{
			startedAt: now,
			requests:  1,
		},
	}

	element = l.lru.PushFront(entry)
	l.clients[clientIP] = element

	return true, 0
}

func (l *IPRateLimiter) evictLRU() {
	element := l.lru.Back()
	if element == nil {
		return
	}

	entry := element.Value.(*clientEntry)

	delete(l.clients, entry.ip)
	l.lru.Remove(element)
}
