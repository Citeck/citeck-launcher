package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
)

// recoveryWriter tracks whether any response has been written.
type recoveryWriter struct {
	http.ResponseWriter
	written bool
}

func (rw *recoveryWriter) WriteHeader(code int) {
	rw.written = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *recoveryWriter) Write(b []byte) (int, error) {
	rw.written = true
	return rw.ResponseWriter.Write(b)
}

func (rw *recoveryWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// RecoveryMiddleware catches panics in handlers and returns 500 with INTERNAL_ERROR code.
// Logs the full stack trace via slog.Error. Applied to both socketMux and tcpMux.
// If the response was already started before the panic, only the log is emitted
// (cannot change the HTTP status after headers are sent).
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &recoveryWriter{ResponseWriter: w}
		defer func() {
			if err := recover(); err != nil {
				stack := debug.Stack()
				slog.Error("Panic recovered in HTTP handler",
					"error", fmt.Sprint(err),
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(stack),
				)
				if !rw.written {
					writeMiddlewareError(w, http.StatusInternalServerError, api.ErrCodeInternalError, "internal server error")
				}
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

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
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, X-Citeck-CSRF")
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

// CSRFMiddleware requires the X-Citeck-CSRF header on all mutating requests (POST/PUT/DELETE).
// Custom headers force CORS preflight → preflight rejected for unknown origins → CSRF prevented.
// Applied to tcpMux only; socket and mTLS connections don't need it.
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete:
			if r.Header.Get("X-Citeck-CSRF") == "" {
				writeMiddlewareError(w, http.StatusForbidden, api.ErrCodeCSRFMissing, "X-Citeck-CSRF header required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
			writeMiddlewareError(w, http.StatusTooManyRequests, api.ErrCodeRateLimited, "rate limit exceeded")
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

// writeMiddlewareError writes a structured JSON error response from middleware.
// Uses the same ErrorDto format as route handlers for API consistency.
func writeMiddlewareError(w http.ResponseWriter, httpCode int, errCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	json.NewEncoder(w).Encode(api.ErrorDto{
		Error:   http.StatusText(httpCode),
		Code:    errCode,
		Message: msg,
	})
}

// generateRequestID returns an 8-character hex string (4 random bytes).
func generateRequestID() string {
	var b [4]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
