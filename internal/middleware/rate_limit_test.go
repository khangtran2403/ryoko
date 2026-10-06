package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIPRateLimiterLimitsAndRefillsBucket(t *testing.T) {
	fixedNow := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	limiter := newTestIPRateLimiter(t, map[string]RateLimitPolicy{
		"login": {Requests: 2, Window: time.Minute},
	})
	limiter.now = func() time.Time { return fixedNow }
	var calls int
	handler := limiter.Limit("login", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))

	for attempt := 1; attempt <= 3; attempt++ {
		recorder := performRateLimitedRequest(handler, "192.0.2.10:4000", "")
		if attempt <= 2 && recorder.Code != http.StatusNoContent {
			t.Fatalf("attempt %d status = %d, want 204", attempt, recorder.Code)
		}
		if attempt == 3 {
			if recorder.Code != http.StatusTooManyRequests {
				t.Fatalf("attempt 3 status = %d, want 429", recorder.Code)
			}
			if got := recorder.Header().Get("Retry-After"); got != "30" {
				t.Errorf("Retry-After = %q, want 30", got)
			}
		}
	}
	if calls != 2 {
		t.Errorf("downstream calls = %d, want 2", calls)
	}

	fixedNow = fixedNow.Add(30 * time.Second)
	if recorder := performRateLimitedRequest(handler, "192.0.2.10:4001", ""); recorder.Code != http.StatusNoContent {
		t.Errorf("refilled request status = %d, want 204", recorder.Code)
	}
}

func TestIPRateLimiterSeparatesIPsAndScopes(t *testing.T) {
	limiter := newTestIPRateLimiter(t, map[string]RateLimitPolicy{
		"login":    {Requests: 1, Window: time.Minute},
		"exchange": {Requests: 1, Window: time.Minute},
	})
	allowed := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	login := limiter.Limit("login", allowed)
	exchange := limiter.Limit("exchange", allowed)

	if got := performRateLimitedRequest(login, "192.0.2.1:1000", "").Code; got != http.StatusNoContent {
		t.Fatalf("first IP status = %d", got)
	}
	if got := performRateLimitedRequest(login, "192.0.2.1:2000", "").Code; got != http.StatusTooManyRequests {
		t.Errorf("same IP with different port status = %d, want 429", got)
	}
	if got := performRateLimitedRequest(login, "192.0.2.2:1000", "").Code; got != http.StatusNoContent {
		t.Errorf("second IP status = %d, want 204", got)
	}
	if got := performRateLimitedRequest(exchange, "192.0.2.1:3000", "").Code; got != http.StatusNoContent {
		t.Errorf("different scope status = %d, want 204", got)
	}
}

func TestIPRateLimiterIgnoresForwardedFor(t *testing.T) {
	limiter := newTestIPRateLimiter(t, map[string]RateLimitPolicy{
		"login": {Requests: 1, Window: time.Minute},
	})
	handler := limiter.Limit("login", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if got := performRateLimitedRequest(handler, "192.0.2.1:1000", "198.51.100.1").Code; got != http.StatusNoContent {
		t.Fatalf("first request status = %d", got)
	}
	if got := performRateLimitedRequest(handler, "192.0.2.1:1001", "203.0.113.9").Code; got != http.StatusTooManyRequests {
		t.Errorf("spoofed forwarded IP status = %d, want 429", got)
	}
}

func TestIPRateLimiterIsConcurrencySafe(t *testing.T) {
	limiter := newTestIPRateLimiter(t, map[string]RateLimitPolicy{
		"login": {Requests: 10, Window: time.Hour},
	})
	var downstreamCalls atomic.Int64
	handler := limiter.Limit("login", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downstreamCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))

	const requests = 100
	statuses := make(chan int, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			remoteAddr := "192.0.2.50:" + strconv.Itoa(1000+port)
			statuses <- performRateLimitedRequest(handler, remoteAddr, "").Code
		}(i)
	}
	wg.Wait()
	close(statuses)
	var allowed, rejected int
	for status := range statuses {
		if status == http.StatusNoContent {
			allowed++
		} else if status == http.StatusTooManyRequests {
			rejected++
		} else {
			t.Fatalf("unexpected status %d", status)
		}
	}
	if allowed != 10 || rejected != 90 || downstreamCalls.Load() != 10 {
		t.Errorf("allowed=%d rejected=%d downstream=%d", allowed, rejected, downstreamCalls.Load())
	}
}

func TestIPRateLimiterCleansIdleBucketsAndCapsGrowth(t *testing.T) {
	fixedNow := time.Now().UTC()
	limiter := newTestIPRateLimiter(t, map[string]RateLimitPolicy{
		"login": {Requests: 1, Window: time.Minute},
	})
	limiter.now = func() time.Time { return fixedNow }
	limiter.maxEntries = 1
	handler := limiter.Limit("login", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if got := performRateLimitedRequest(handler, "192.0.2.1:1", "").Code; got != http.StatusNoContent {
		t.Fatalf("first status = %d", got)
	}
	if got := performRateLimitedRequest(handler, "192.0.2.2:1", "").Code; got != http.StatusTooManyRequests {
		t.Errorf("capacity status = %d, want 429", got)
	}
	fixedNow = fixedNow.Add(time.Minute)
	if got := performRateLimitedRequest(handler, "192.0.2.2:1", "").Code; got != http.StatusNoContent {
		t.Errorf("status after cleanup = %d, want 204", got)
	}
}

func TestNewIPRateLimiterValidatesConfiguration(t *testing.T) {
	invalid := []map[string]RateLimitPolicy{
		nil,
		{"": {Requests: 1, Window: time.Minute}},
		{"login": {Requests: 0, Window: time.Minute}},
		{"login": {Requests: 1, Window: 0}},
	}
	for _, policies := range invalid {
		if _, err := NewIPRateLimiter(policies, time.Minute); err == nil {
			t.Errorf("NewIPRateLimiter(%+v) returned nil error", policies)
		}
	}
	if _, err := NewIPRateLimiter(map[string]RateLimitPolicy{"login": {Requests: 1, Window: time.Minute}}, 0); err == nil {
		t.Error("NewIPRateLimiter() with zero cleanup interval returned nil error")
	}
}

func newTestIPRateLimiter(t *testing.T, policies map[string]RateLimitPolicy) *IPRateLimiter {
	t.Helper()
	limiter, err := NewIPRateLimiter(policies, 30*time.Second)
	if err != nil {
		t.Fatalf("NewIPRateLimiter() error = %v", err)
	}
	return limiter
}

func performRateLimitedRequest(handler http.Handler, remoteAddr, forwardedFor string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/auth", nil)
	request.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
