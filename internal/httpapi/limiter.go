package httpapi

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	maxLimiterClients = 10_000
	clientIdleTTL     = 10 * time.Minute
	cleanupInterval   = 5 * time.Minute
)

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type ClientLimiter struct {
	mu         sync.Mutex
	stopOnce   sync.Once
	clients    map[string]*limiterEntry
	limit      rate.Limit
	burst      int
	retryAfter string
	now        func() time.Time
	stop       chan struct{}
	done       chan struct{}
}

func NewClientLimiter(requestsPerSecond float64, burst int) *ClientLimiter {
	retryAfterSeconds := max(1, int(math.Ceil(1/requestsPerSecond)))
	limiter := &ClientLimiter{
		clients:    make(map[string]*limiterEntry),
		limit:      rate.Limit(requestsPerSecond),
		burst:      burst,
		retryAfter: strconv.Itoa(retryAfterSeconds),
		now:        time.Now,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	go limiter.cleanupLoop()
	return limiter
}

func (l *ClientLimiter) Allow(remoteAddress string) bool {
	clientIP := remoteAddress
	if host, _, err := net.SplitHostPort(remoteAddress); err == nil {
		clientIP = host
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry, ok := l.clients[clientIP]
	if !ok {
		if len(l.clients) >= maxLimiterClients {
			return false
		}
		entry = &limiterEntry{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.clients[clientIP] = entry
	}
	entry.lastSeen = now
	return entry.limiter.Allow()
}

func (l *ClientLimiter) Close() {
	l.stopOnce.Do(func() {
		close(l.stop)
		<-l.done
	})
}

func (l *ClientLimiter) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	defer close(l.done)

	for {
		select {
		case <-ticker.C:
			l.removeIdle()
		case <-l.stop:
			return
		}
	}
}

func (l *ClientLimiter) removeIdle() {
	cutoff := l.now().Add(-clientIdleTTL)
	l.mu.Lock()
	defer l.mu.Unlock()
	for clientIP, entry := range l.clients {
		if entry.lastSeen.Before(cutoff) {
			delete(l.clients, clientIP)
		}
	}
}

func (l *ClientLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			next.ServeHTTP(writer, request)
			return
		}
		if !l.Allow(request.RemoteAddr) {
			writer.Header().Set("Retry-After", l.retryAfter)
			writeError(writer, http.StatusTooManyRequests, "rate_limited", "request rate limit exceeded")
			return
		}
		next.ServeHTTP(writer, request)
	})
}
