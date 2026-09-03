package httpapi

import (
	"container/list"
	"math"
	"net"
	"net/http"
	"net/netip"
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
	key      string
	element  *list.Element
}

type ClientLimiter struct {
	mu         sync.Mutex
	stopOnce   sync.Once
	clients    map[string]*limiterEntry
	lru        *list.List
	maxClients int
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
		lru:        list.New(),
		maxClients: maxLimiterClients,
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
	clientIP := clientKey(remoteAddress)

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry, ok := l.clients[clientIP]
	if !ok {
		if len(l.clients) >= l.maxClients {
			oldest := l.lru.Back()
			if oldest != nil {
				oldestEntry := oldest.Value.(*limiterEntry)
				delete(l.clients, oldestEntry.key)
				l.lru.Remove(oldest)
			}
		}
		entry = &limiterEntry{limiter: rate.NewLimiter(l.limit, l.burst), key: clientIP}
		entry.element = l.lru.PushFront(entry)
		l.clients[clientIP] = entry
	} else {
		l.lru.MoveToFront(entry.element)
	}
	entry.lastSeen = now
	return entry.limiter.Allow()
}

func clientKey(remoteAddress string) string {
	host := remoteAddress
	if parsedHost, _, err := net.SplitHostPort(remoteAddress); err == nil {
		host = parsedHost
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	address = address.Unmap()
	if address.Is6() {
		return netip.PrefixFrom(address, 64).Masked().String()
	}
	return address.String()
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
	for element := l.lru.Back(); element != nil; {
		entry := element.Value.(*limiterEntry)
		if !entry.lastSeen.Before(cutoff) {
			break
		}
		previous := element.Prev()
		delete(l.clients, entry.key)
		l.lru.Remove(element)
		element = previous
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

func (l *ClientLimiter) MiddlewareAll(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !l.Allow(request.RemoteAddr) {
			writer.Header().Set("Retry-After", l.retryAfter)
			writeError(writer, http.StatusTooManyRequests, "rate_limited", "request rate limit exceeded")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

type GlobalGuard struct {
	limiter    *rate.Limiter
	retryAfter string
	slots      chan struct{}
}

func NewGlobalGuard(requestsPerSecond float64, burst, maxConcurrent int) *GlobalGuard {
	return &GlobalGuard{
		limiter:    rate.NewLimiter(rate.Limit(requestsPerSecond), burst),
		retryAfter: strconv.Itoa(max(1, int(math.Ceil(1/requestsPerSecond)))),
		slots:      make(chan struct{}, maxConcurrent),
	}
}

func (g *GlobalGuard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !g.limiter.Allow() {
			writer.Header().Set("Retry-After", g.retryAfter)
			writeError(writer, http.StatusTooManyRequests, "global_rate_limited", "global request rate limit exceeded")
			return
		}
		select {
		case g.slots <- struct{}{}:
			defer func() { <-g.slots }()
			next.ServeHTTP(writer, request)
		default:
			writer.Header().Set("Retry-After", "1")
			writeError(writer, http.StatusServiceUnavailable, "service_busy", "service is temporarily busy")
		}
	})
}
