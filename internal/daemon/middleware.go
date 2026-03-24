package daemon

import (
	"net"
	"net/http"
	"strings"
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
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] != token {
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

// CORSMiddleware adds CORS headers for web UI development.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs HTTP requests.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip logging for static files
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
