package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RateLimiter implements a simple in-memory rate limiter
// For production, use Redis-based rate limiting
type rateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rate     int           // requests allowed
	window   time.Duration // time window
	burst    int           // burst size
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

	// Cleanup old visitors every minute
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

	// Token bucket algorithm
	now := time.Now()
	elapsed := now.Sub(v.lastCheck)
	v.lastCheck = now

	// Add tokens based on elapsed time
	rate := float64(rl.rate) / float64(rl.window.Seconds())
	v.tokens += elapsed.Seconds() * rate

	// Cap at burst size
	if v.tokens > float64(rl.burst) {
		v.tokens = float64(rl.burst)
	}

	if v.tokens < 1 {
		return false
	}

	v.tokens--
	return true
}

// Global rate limiter instance
var globalLimiter *rateLimiter

// RateLimiter middleware for global rate limiting
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

// Endpoint-specific rate limiters
var endpointLimiters = make(map[string]*rateLimiter)
var endpointLimitersMu sync.Mutex

// RateLimitEndpoint creates rate limiting for specific endpoints
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

		// Use IP + user/session as key for per-user limiting
		key := c.ClientIP()
		if userID, exists := c.Get("user_id"); exists {
			if uid, ok := userID.(uuid.UUID); ok {
				key = uid.String()
			} else if uidStr, ok := userID.(string); ok {
				key = uidStr
			}
		} else if sessionID, exists := c.Get("session_id"); exists {
			key = sessionID.(string)
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

// AbuseDetection middleware for detecting suspicious patterns
func AbuseDetection() gin.HandlerFunc {
	// Track suspicious patterns
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
			// Check if activity is suspicious
			if activity.failedLogins > 10 ||
				activity.flagAttempts > 50 ||
				activity.scanPatterns > 100 {

				// If last activity was recent, block
				if time.Since(activity.lastActivity) < 15*time.Minute {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
						"error": "Suspicious activity detected. Please try again later.",
					})
					return
				}

				// Reset after cooldown
				mu.Lock()
				delete(suspects, ip)
				mu.Unlock()
			}
		}

		c.Next()

		// Record failed attempts after request
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
