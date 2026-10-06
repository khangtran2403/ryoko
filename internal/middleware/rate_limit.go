package middleware

import (
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultRateLimitMaxEntries = 50_000

type RateLimitPolicy struct {
	Requests int
	Window   time.Duration
}

type rateLimitBucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
	window     time.Duration
}

type IPRateLimiter struct {
	mu              sync.Mutex
	policies        map[string]RateLimitPolicy
	buckets         map[string]*rateLimitBucket
	cleanupInterval time.Duration
	lastCleanup     time.Time
	maxEntries      int
	now             func() time.Time
}

func NewIPRateLimiter(
	policies map[string]RateLimitPolicy,
	cleanupInterval time.Duration,
) (*IPRateLimiter, error) {
	if len(policies) == 0 {
		return nil, errors.New("at least one rate-limit policy is required")
	}
	if cleanupInterval <= 0 {
		return nil, errors.New("rate-limit cleanup interval must be positive")
	}
	policiesCopy := make(map[string]RateLimitPolicy, len(policies))
	for scope, policy := range policies {
		scope = strings.TrimSpace(scope)
		if scope == "" || policy.Requests <= 0 || policy.Window <= 0 {
			return nil, errors.New("rate-limit scope, request count, and window must be valid")
		}
		policiesCopy[scope] = policy
	}
	return &IPRateLimiter{
		policies: policiesCopy, buckets: make(map[string]*rateLimitBucket),
		cleanupInterval: cleanupInterval, maxEntries: defaultRateLimitMaxEntries,
		now: time.Now,
	}, nil
}

func (l *IPRateLimiter) Limit(scope string, next http.Handler) http.Handler {
	policy, ok := l.policies[scope]
	if !ok {
		panic("unknown rate-limit scope: " + scope)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter := l.allow(scope, clientPeerIP(r.RemoteAddr), policy)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *IPRateLimiter) allow(scope, peerIP string, policy RateLimitPolicy) (bool, time.Duration) {
	now := l.now().UTC()
	key := scope + "\x00" + peerIP
	ratePerSecond := float64(policy.Requests) / policy.Window.Seconds()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastCleanup.IsZero() || now.Sub(l.lastCleanup) >= l.cleanupInterval {
		l.cleanupLocked(now)
	}

	bucket, exists := l.buckets[key]
	if !exists {
		if len(l.buckets) >= l.maxEntries {
			return false, l.cleanupInterval
		}
		bucket = &rateLimitBucket{
			tokens: float64(policy.Requests), lastRefill: now,
			lastSeen: now, window: policy.Window,
		}
		l.buckets[key] = bucket
	}

	elapsed := now.Sub(bucket.lastRefill).Seconds()
	if elapsed > 0 {
		bucket.tokens = math.Min(float64(policy.Requests), bucket.tokens+elapsed*ratePerSecond)
		bucket.lastRefill = now
	}
	bucket.lastSeen = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}

	waitSeconds := (1 - bucket.tokens) / ratePerSecond
	retryAfter := time.Duration(math.Ceil(waitSeconds*1000)) * time.Millisecond
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return false, retryAfter
}

func (l *IPRateLimiter) cleanupLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.lastSeen) >= bucket.window {
			delete(l.buckets, key)
		}
	}
	l.lastCleanup = now
}

func clientPeerIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil && host != "" {
		return host
	}
	if parsed := net.ParseIP(strings.TrimSpace(remoteAddr)); parsed != nil {
		return parsed.String()
	}
	return "unknown"
}
