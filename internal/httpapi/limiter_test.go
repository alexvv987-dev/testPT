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

func TestClientLimiterAggregatesIPv6AndEvictsLRU(t *testing.T) {
	limiter := NewClientLimiter(1, 1)
	t.Cleanup(limiter.Close)
	limiter.maxClients = 2

	if !limiter.Allow("[2001:db8:1:2::1]:1000") {
		t.Fatal("first IPv6 request unexpectedly rejected")
	}
	if limiter.Allow("[2001:db8:1:2::2]:2000") {
		t.Fatal("same IPv6 /64 received a separate bucket")
	}
	if !limiter.Allow("192.0.2.1:1000") {
		t.Fatal("IPv4 request unexpectedly rejected")
	}
	if !limiter.Allow("198.51.100.1:1000") {
		t.Fatal("new client unexpectedly rejected at capacity")
	}

	limiter.mu.Lock()
	_, oldClientPresent := limiter.clients["2001:db8:1:2::/64"]
	_, newClientPresent := limiter.clients["198.51.100.1"]
	limiter.mu.Unlock()
	if oldClientPresent || !newClientPresent {
		t.Fatalf("LRU eviction state: old=%v new=%v", oldClientPresent, newClientPresent)
	}
}

func TestClientLimiterMiddlewareAll(t *testing.T) {
	limiter := NewClientLimiter(0.25, 1)
	t.Cleanup(limiter.Close)
	handler := limiter.MiddlewareAll(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	for attempt, want := range []int{http.StatusNoContent, http.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodGet, "/abc123", nil)
		request.RemoteAddr = "192.0.2.1:1000"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, recorder.Code, want)
		}
	}
}

func TestGlobalGuardRateAndConcurrency(t *testing.T) {
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	rateGuard := NewGlobalGuard(0.25, 1, 1)
	rateHandler := rateGuard.Middleware(next)
	for attempt, want := range []int{http.StatusNoContent, http.StatusTooManyRequests} {
		recorder := httptest.NewRecorder()
		rateHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if recorder.Code != want {
			t.Fatalf("rate attempt %d status = %d, want %d", attempt, recorder.Code, want)
		}
	}

	concurrencyGuard := NewGlobalGuard(10_000, 10, 1)
	concurrencyGuard.slots <- struct{}{}
	recorder := httptest.NewRecorder()
	concurrencyGuard.Middleware(next).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	<-concurrencyGuard.slots
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("concurrency status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestClientLimitersRejectBeforeGlobalLimiter(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		postRate  float64
		postBurst int
		readRate  float64
		readBurst int
	}{
		{name: "read limiter", method: http.MethodGet, postRate: 10_000, postBurst: 100, readRate: 0.001, readBurst: 1},
		{name: "post limiter", method: http.MethodPost, postRate: 0.001, postBurst: 1, readRate: 10_000, readBurst: 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			postLimiter := NewClientLimiter(test.postRate, test.postBurst)
			readLimiter := NewClientLimiter(test.readRate, test.readBurst)
			t.Cleanup(postLimiter.Close)
			t.Cleanup(readLimiter.Close)
			globalGuard := NewGlobalGuard(0.001, 2, 10)

			var handler http.Handler = http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			})
			handler = globalGuard.Middleware(handler)
			handler = postLimiter.Middleware(handler)
			handler = readLimiter.MiddlewareAll(handler)

			for _, request := range []struct {
				remoteAddress string
				wantStatus    int
			}{
				{remoteAddress: "192.0.2.1:1000", wantStatus: http.StatusNoContent},
				{remoteAddress: "192.0.2.1:2000", wantStatus: http.StatusTooManyRequests},
				{remoteAddress: "198.51.100.1:1000", wantStatus: http.StatusNoContent},
			} {
				httpRequest := httptest.NewRequest(test.method, "/", nil)
				httpRequest.RemoteAddr = request.remoteAddress
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httpRequest)
				if recorder.Code != request.wantStatus {
					t.Fatalf("request from %s status = %d, want %d", request.remoteAddress, recorder.Code, request.wantStatus)
				}
			}
		})
	}
}
