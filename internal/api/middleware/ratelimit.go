package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// the api runs as a single stateful control-plane process, so a process-local
// limiter avoids an external dependency on the request path.
type rateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rate     int
	window   time.Duration
	burst    int
}

type visitor struct {
	tokens    float64
	lastCheck time.Time
}

func newRateLimiter(rate int, window time.Duration, burst int) *rateLimiter {
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
		burst:    burst,
	}

	go rl.cleanup()

	return rl
}

func (rl *rateLimiter) cleanup() {
	for {
		time.Sleep(time.Minute)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastCheck) > rl.window*2 {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	if !exists {
		rl.visitors[key] = &visitor{
			tokens:    float64(rl.burst) - 1,
			lastCheck: time.Now(),
		}
		return true
	}

	// token bucket
	now := time.Now()
	elapsed := now.Sub(v.lastCheck)
	v.lastCheck = now

	rate := float64(rl.rate) / float64(rl.window.Seconds())
	v.tokens += elapsed.Seconds() * rate

	if v.tokens > float64(rl.burst) {
		v.tokens = float64(rl.burst)
	}

	if v.tokens < 1 {
		return false
	}

	v.tokens--
	return true
}

var globalLimiter *rateLimiter

func RateLimiter(cfg config.RateLimitConfig) gin.HandlerFunc {
	globalLimiter = newRateLimiter(
		cfg.RequestsPerMinute,
		time.Minute,
		cfg.BurstSize,
	)

	return func(c *gin.Context) {
		key := c.ClientIP()

		if !globalLimiter.allow(key) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "Rate limit exceeded",
				"retry_after": 60,
			})
			return
		}

		c.Next()
	}
}

var endpointLimiters = make(map[string]*rateLimiter)
var endpointLimitersMu sync.Mutex

func RateLimitEndpoint(cfg config.RateLimit) gin.HandlerFunc {
	return func(c *gin.Context) {
		endpoint := c.FullPath()

		endpointLimitersMu.Lock()
		limiter, exists := endpointLimiters[endpoint]
		if !exists {
			limiter = newRateLimiter(cfg.Requests, cfg.Window, cfg.Requests)
			endpointLimiters[endpoint] = limiter
		}
		endpointLimitersMu.Unlock()

		// key on user/session when available so limits are per-user, not per-ip
		key := c.ClientIP()
		if userID, exists := c.Get("user_id"); exists {
			if uid, ok := userID.(uuid.UUID); ok {
				key = uid.String()
			} else if uidStr, ok := userID.(string); ok {
				key = uidStr
			}
		} else if sessionID, exists := c.Get("session_id"); exists {
			if sid, ok := sessionID.(uuid.UUID); ok {
				key = sid.String()
			} else if sidStr, ok := sessionID.(string); ok {
				key = sidStr
			}
		}

		if !limiter.allow(key) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "Rate limit exceeded for this action",
				"retry_after": int(cfg.Window.Seconds()),
			})
			return
		}

		c.Next()
	}
}

func AbuseDetection() gin.HandlerFunc {
	type suspiciousActivity struct {
		failedLogins int
		flagAttempts int
		scanPatterns int
		lastActivity time.Time
	}

	suspects := make(map[string]*suspiciousActivity)
	var mu sync.RWMutex

	return func(c *gin.Context) {
		ip := c.ClientIP()

		mu.RLock()
		activity, exists := suspects[ip]
		mu.RUnlock()

		if exists {
			if activity.failedLogins > 10 ||
				activity.flagAttempts > 50 ||
				activity.scanPatterns > 100 {

				if time.Since(activity.lastActivity) < 15*time.Minute {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
						"error": "Suspicious activity detected. Please try again later.",
					})
					return
				}

				mu.Lock()
				delete(suspects, ip)
				mu.Unlock()
			}
		}

		c.Next()

		if c.Writer.Status() == http.StatusUnauthorized {
			mu.Lock()
			if _, exists := suspects[ip]; !exists {
				suspects[ip] = &suspiciousActivity{}
			}
			suspects[ip].failedLogins++
			suspects[ip].lastActivity = time.Now()
			mu.Unlock()
		}
	}
}
