package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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

func (rw *recoveryWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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

// SecurityHeadersMiddleware adds browser security headers to all responses.
func SecurityHeadersMiddleware(mtlsActive bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if mtlsActive {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000")
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

func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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

		// Record metrics
		httpMetrics.record(r.Method, r.URL.Path, rec.status, time.Since(start))
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

// --- HTTP request metrics ---

// pathParamRe matches path segments that look like dynamic parameters (UUIDs, names with dots/dashes).
var pathParamRe = regexp.MustCompile(`/api/v1/(?:apps|namespaces|volumes|snapshots)/([^/]+)`)

// normalizePath replaces dynamic path segments with :name to limit cardinality.
func normalizePath(path string) string {
	return pathParamRe.ReplaceAllStringFunc(path, func(m string) string {
		// Keep the prefix, replace the captured segment
		idx := strings.LastIndex(m, "/")
		return m[:idx] + "/:name"
	})
}

// metricsKey uniquely identifies a request counter bucket.
type metricsKey struct {
	method string
	path   string
	status int
}

// histogramKey identifies a latency histogram bucket.
type histogramKey struct {
	path string
}

var histogramBuckets = []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 30}

const numHistogramBuckets = 7 // must match len(histogramBuckets)

type histogramData struct {
	buckets [numHistogramBuckets]atomic.Int64
	count   atomic.Int64
	sum     atomic.Int64 // sum in microseconds (avoids float atomics)
}

func init() {
	if len(histogramBuckets) != numHistogramBuckets {
		panic("histogramBuckets length mismatch with numHistogramBuckets")
	}
}

// metricsCollector tracks HTTP request metrics without external dependencies.
type metricsCollector struct {
	mu         sync.RWMutex
	counters   map[metricsKey]*atomic.Int64
	histograms map[histogramKey]*histogramData
}

var httpMetrics = &metricsCollector{
	counters:   make(map[metricsKey]*atomic.Int64),
	histograms: make(map[histogramKey]*histogramData),
}

func (mc *metricsCollector) record(method, path string, status int, duration time.Duration) {
	normalized := normalizePath(path)

	// Counter
	ck := metricsKey{method: method, path: normalized, status: status}
	mc.mu.RLock()
	counter := mc.counters[ck]
	mc.mu.RUnlock()
	if counter == nil {
		mc.mu.Lock()
		counter = mc.counters[ck]
		if counter == nil {
			counter = &atomic.Int64{}
			mc.counters[ck] = counter
		}
		mc.mu.Unlock()
	}
	counter.Add(1)

	// Histogram
	hk := histogramKey{path: normalized}
	mc.mu.RLock()
	hist := mc.histograms[hk]
	mc.mu.RUnlock()
	if hist == nil {
		mc.mu.Lock()
		hist = mc.histograms[hk]
		if hist == nil {
			hist = &histogramData{}
			mc.histograms[hk] = hist
		}
		mc.mu.Unlock()
	}
	secs := duration.Seconds()
	for i, bound := range histogramBuckets {
		if secs <= bound {
			hist.buckets[i].Add(1)
			break // only the tightest bucket; writePrometheus cumulates on output
		}
	}
	hist.count.Add(1)
	hist.sum.Add(int64(math.Round(secs * 1e6))) // store as microseconds
}

// writePrometheus appends HTTP metrics in Prometheus text exposition format.
func (mc *metricsCollector) writePrometheus(b *strings.Builder) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if len(mc.counters) > 0 {
		b.WriteString("# HELP citeck_http_requests_total Total HTTP requests.\n")
		b.WriteString("# TYPE citeck_http_requests_total counter\n")
		// Sort keys for deterministic output
		keys := make([]metricsKey, 0, len(mc.counters))
		for k := range mc.counters {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].path != keys[j].path {
				return keys[i].path < keys[j].path
			}
			if keys[i].method != keys[j].method {
				return keys[i].method < keys[j].method
			}
			return keys[i].status < keys[j].status
		})
		for _, k := range keys {
			fmt.Fprintf(b, "citeck_http_requests_total{method=\"%s\",path=\"%s\",status=\"%d\"} %d\n",
				promEscape(k.method), promEscape(k.path), k.status, mc.counters[k].Load())
		}
	}

	if len(mc.histograms) > 0 {
		b.WriteString("# HELP citeck_http_request_duration_seconds HTTP request latency.\n")
		b.WriteString("# TYPE citeck_http_request_duration_seconds histogram\n")
		hkeys := make([]string, 0, len(mc.histograms))
		for k := range mc.histograms {
			hkeys = append(hkeys, k.path)
		}
		sort.Strings(hkeys)
		for _, path := range hkeys {
			hist := mc.histograms[histogramKey{path: path}]
			ep := promEscape(path)
			var cumulative int64
			for i, bound := range histogramBuckets {
				cumulative += hist.buckets[i].Load()
				fmt.Fprintf(b, "citeck_http_request_duration_seconds_bucket{path=\"%s\",le=\"%.2f\"} %d\n", ep, bound, cumulative)
			}
			fmt.Fprintf(b, "citeck_http_request_duration_seconds_bucket{path=\"%s\",le=\"+Inf\"} %d\n", ep, hist.count.Load())
			fmt.Fprintf(b, "citeck_http_request_duration_seconds_count{path=\"%s\"} %d\n", ep, hist.count.Load())
			fmt.Fprintf(b, "citeck_http_request_duration_seconds_sum{path=\"%s\"} %.6f\n", ep, float64(hist.sum.Load())/1e6)
		}
	}
}
