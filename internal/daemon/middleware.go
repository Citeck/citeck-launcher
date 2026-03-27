package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MTLSIdentityMiddleware extracts the client identity from mTLS peer certificates.
// Logs the CN of the authenticated client for auditing.
func MTLSIdentityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			cn := r.TLS.PeerCertificates[0].Subject.CommonName
			slog.Debug("mTLS client authenticated", "cn", cn, "remote", r.RemoteAddr)
		}
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware validates Origin against the exact daemon listen address.
// Only the Web UI served by this daemon should be allowed — no prefix matching.
func CORSMiddleware(next http.Handler, listenAddr string) http.Handler {
	allowed := buildCORSAllowedOrigins(listenAddr)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
			w.Header().Set("Vary", "Origin")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		} else if r.Method == "OPTIONS" {
			// Reject preflight from unknown origins
			w.WriteHeader(http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// buildCORSAllowedOrigins creates the set of exact origins allowed for the daemon.
func buildCORSAllowedOrigins(listenAddr string) map[string]bool {
	origins := make(map[string]bool)
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return origins
	}
	// Normalize host
	if host == "" || host == "0.0.0.0" || host == "::" {
		// Bound to all interfaces — allow both localhost aliases with the exact port
		for _, h := range []string{"127.0.0.1", "localhost"} {
			origins["http://"+h+":"+port] = true
			origins["https://"+h+":"+port] = true
		}
	} else {
		origins["http://"+host+":"+port] = true
		origins["https://"+host+":"+port] = true
	}
	return origins
}

// RateLimitMiddleware limits requests per IP using a token bucket with automatic eviction.
func RateLimitMiddleware(rps int, next http.Handler) http.Handler {
	var mu sync.Mutex
	limiters := make(map[string]*rateLimiterEntry)
	const maxEntries = 1000
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

// Unwrap allows http.ResponseController to reach the underlying ResponseWriter
// for SetWriteDeadline and other per-connection controls.
func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// LoggingMiddleware logs HTTP requests with method, path, status, duration, remote addr, request ID, and mTLS CN.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip logging for static files
		if strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/favicon.ico" {
			next.ServeHTTP(w, r)
			return
		}

		// Generate and set X-Request-Id
		reqID := generateRequestID()
		w.Header().Set("X-Request-Id", reqID)

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).String(),
			"remote", r.RemoteAddr,
			"reqId", reqID,
		}
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			attrs = append(attrs, "cn", r.TLS.PeerCertificates[0].Subject.CommonName)
		}
		slog.Info("HTTP request", attrs...)
	})
}

// generateRequestID returns an 8-character hex string (4 random bytes).
func generateRequestID() string {
	var b [4]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
