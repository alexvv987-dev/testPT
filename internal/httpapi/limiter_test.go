package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientLimiterMiddleware(t *testing.T) {
	limiter := NewClientLimiter(0.25, 1)
	t.Cleanup(limiter.Close)
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := limiter.Middleware(next)

	first := httptest.NewRequest(http.MethodPost, "/shorten", nil)
	first.RemoteAddr = "192.0.2.1:1000"
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusNoContent {
		t.Fatalf("first status = %d", firstRecorder.Code)
	}

	second := httptest.NewRequest(http.MethodPost, "/shorten", nil)
	second.RemoteAddr = "192.0.2.1:2000"
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusTooManyRequests || secondRecorder.Header().Get("Retry-After") != "4" {
		t.Fatalf("second status = %d, Retry-After = %q", secondRecorder.Code, secondRecorder.Header().Get("Retry-After"))
	}

	get := httptest.NewRequest(http.MethodGet, "/shorten", nil)
	get.RemoteAddr = "192.0.2.1:3000"
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusNoContent {
		t.Fatalf("GET status = %d, rate limit must only apply to POST", getRecorder.Code)
	}
}

func TestClientLimiterRemovesIdleEntries(t *testing.T) {
	limiter := NewClientLimiter(1, 1)
	t.Cleanup(limiter.Close)
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	if !limiter.Allow("192.0.2.1:1000") {
		t.Fatal("first request unexpectedly rejected")
	}

	now = now.Add(clientIdleTTL + time.Second)
	limiter.removeIdle()
	limiter.mu.Lock()
	count := len(limiter.clients)
	limiter.mu.Unlock()
	if count != 0 {
		t.Fatalf("client count = %d, want 0", count)
	}
}
