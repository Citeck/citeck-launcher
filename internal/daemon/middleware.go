package daemon

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TokenAuthMiddleware checks for a valid Bearer token on TCP connections.
// Unix socket connections skip authentication.
func TokenAuthMiddleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next // No token configured, skip auth
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for Unix socket connections (local)
		if isUnixSocket(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Check Authorization header
		auth := r.Header.Get("Authorization")
		if auth == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isUnixSocket detects if the connection came via Unix domain socket.
func isUnixSocket(r *http.Request) bool {
	// If the remote address is empty or a Unix path, it's a local connection
	if r.RemoteAddr == "" {
		return true
	}
	// Unix socket connections typically have no port
	_, _, err := net.SplitHostPort(r.RemoteAddr)
	return err != nil
}

// defaultCORSOrigins lists origins allowed by default for the web UI.
var defaultCORSOrigins = []string{
	"http://127.0.0.1",
	"http://localhost",
}

// CORSMiddleware validates Origin against allowed patterns and reflects the matching origin.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && matchCORSOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func matchCORSOrigin(origin string) bool {
	for _, allowed := range defaultCORSOrigins {
		if origin == allowed || strings.HasPrefix(origin, allowed+":") {
			return true
		}
	}
	return false
}

// RateLimitMiddleware limits requests per IP using a token bucket with automatic eviction.
func RateLimitMiddleware(rps int, next http.Handler) http.Handler {
	var mu sync.Mutex
	limiters := make(map[string]*rateLimiterEntry)
	const maxEntries = 10000
	const evictAge = 5 * time.Minute

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}

		now := time.Now()
		mu.Lock()

		// Evict stale entries when map grows too large
		if len(limiters) > maxEntries {
			for k, v := range limiters {
				if now.Sub(v.last) > evictAge {
					delete(limiters, k)
				}
			}
		}

		entry, ok := limiters[ip]
		if !ok {
			entry = &rateLimiterEntry{tokens: float64(rps), last: now}
			limiters[ip] = entry
		}
		// Refill tokens
		elapsed := now.Sub(entry.last).Seconds()
		entry.tokens += elapsed * float64(rps)
		if entry.tokens > float64(rps) {
			entry.tokens = float64(rps)
		}
		entry.last = now
		if entry.tokens < 1 {
			mu.Unlock()
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		entry.tokens--
		mu.Unlock()

		next.ServeHTTP(w, r)
	})
}

type rateLimiterEntry struct {
	tokens float64
	last   time.Time
}

// statusRecorder captures the HTTP status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// LoggingMiddleware logs HTTP requests with method, path, status, and duration.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip logging for static files
		if strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/favicon.ico" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("HTTP request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start).String())
	})
}
