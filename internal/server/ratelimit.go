package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rate limiting for the routes anyone on the internet can reach.
//
// This mattered little while Cloudflare Access held an allowlist at the edge:
// four addresses could not do much harm. With that gone, the sign-in and
// registration flows are open to everyone, and each completed registration is a
// row in Postgres and two emails from an address whose reputation is worth
// keeping. Nothing here stops a determined person; it stops a loop.
//
// Deliberately in-process and approximate. A shared limiter would need somewhere
// to share state, which is a database or a queue, which is a bigger thing to run
// and to break than the problem justifies for one replica.

// bucket is a token bucket: burst tokens, refilled at rate per second.
type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	rate  float64 // tokens per second
	burst float64

	mu   sync.Mutex
	by   map[string]*bucket
	seen time.Time // last sweep of idle buckets
}

func newLimiter(perMinute, burst int) *limiter {
	return &limiter{
		rate:  float64(perMinute) / 60,
		burst: float64(burst),
		by:    map[string]*bucket{},
		seen:  time.Now(),
	}
}

// allow reports whether this key may proceed, and takes a token if so.
func (l *limiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Drop buckets nobody has touched in a while, so a stream of one-request
	// addresses cannot grow this map without bound — which would turn a defence
	// against abuse into a way to exhaust memory.
	if now.Sub(l.seen) > 10*time.Minute {
		for k, b := range l.by {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.by, k)
			}
		}
		l.seen = now
	}

	b, ok := l.by[key]
	if !ok {
		l.by[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// limit refuses a caller that is asking too often, keyed by client address.
func (l *limiter) limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			// Tell them when to come back rather than just refusing: a client
			// that is looping benignly can behave, and one that is not was
			// never going to read it.
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is who to hold responsible for a request.
//
// Behind Cloudflare the connecting address is Cloudflare's, so every visitor
// would share one bucket and the first busy person would lock out the rest.
// CF-Connecting-IP is set by the proxy and overwritten if a client sends its
// own, which is what makes it usable here — unlike X-Forwarded-For, which is
// appended to and therefore partly attacker-written.
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
