package launchermcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MaxBytes bounds MCP bodies and tool results independently of source read bounds.
const MaxBytes = 256 << 10

// Target is an expected active workspace/namespace pair, not an ACL.
type Target struct {
	WorkspaceID string `json:"workspaceId"`
	NamespaceID string `json:"namespaceId"`
}

// AppSettings is the complete writable runtime-settings allowlist.
type AppSettings struct {
	Memory      *string            `json:"memory,omitempty"`
	CPU         *float64           `json:"cpu,omitempty"`
	JavaOpts    *string            `json:"javaOpts,omitempty"`
	Profiles    *string            `json:"profiles,omitempty"`
	Environment map[string]*string `json:"environment,omitempty"`
	Ports       *[]string          `json:"ports,omitempty"`
}

// Arguments contains only typed tool data; no generic URL/method/API escape hatch.
type Arguments struct {
	Target
	App                    string       `json:"app,omitempty"`
	DestinationWorkspaceID string       `json:"destinationWorkspaceId,omitempty"`
	DestinationNamespaceID string       `json:"destinationNamespaceId,omitempty"`
	Tail                   int          `json:"tail,omitempty"`
	Settings               *AppSettings `json:"settings,omitempty"`
	Path                   string       `json:"path,omitempty"`
	Content                string       `json:"content,omitempty"`
	Command                string       `json:"command,omitempty"`
	Args                   []string     `json:"args,omitempty"`
	HeapFile               string       `json:"heapFile,omitempty"`
}

// Backend receives only this closed tool vocabulary; it is not a generic API
// proxy. It must check scoped targets under the operation's synchronization.
type Backend interface {
	Call(context.Context, string, Arguments) (any, error)
}
type credentialKey struct{}
type requestContextKey struct{}

// Service owns the separate loopback listener and token-bound diagnostic handles.
type Service struct {
	mu             sync.Mutex
	store          *Store
	backend        Backend
	server         *http.Server
	listener       net.Listener
	port           int
	lastError      string
	artifacts      artifactStore
	startupError   error
	retryCancel    context.CancelFunc
	retryDone      chan struct{}
	requestMu      sync.Mutex
	requests       sync.WaitGroup
	requestContext context.Context
	requestCancel  context.CancelFunc
	localeMu       sync.Mutex
	oauthLocale    string
	oauthRateMu    sync.Mutex
	oauthRates     map[string]oauthRateWindow
	closed         bool
}

// New constructs a disabled listener and cleans only orphaned temporary system ZIPs.
func New(store *Store, backend Backend) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{store: store, backend: backend, requestContext: ctx, requestCancel: cancel}
	s.startupError = s.cleanupTemporaryArtifacts()
	if s.startupError != nil {
		s.lastError = "MCP temporary artifact cleanup failed"
	}
	// Revocation is available over the trusted management API even while MCP is
	// disabled. Its persistence retry belongs to the service, not the listener.
	retryCtx, retryCancel := context.WithCancel(context.Background())
	s.retryCancel = retryCancel
	s.retryDone = make(chan struct{})
	go func() { defer close(s.retryDone); s.store.runOAuthRevocationRetry(retryCtx) }()
	return s
}

// Status distinguishes persisted enable intent from actual listener availability.
type Status struct {
	Settings
	Listening    bool    `json:"listening"`
	Endpoint     string  `json:"endpoint"`
	Error        string  `json:"error,omitempty"`
	Tokens       []Token `json:"tokens"`
	OAuthWarning string  `json:"oauthWarning,omitempty"`
}

// Status returns metadata only, including actual bind failures.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.store.Settings()
	return Status{Settings: v, Listening: s.server != nil, Endpoint: fmt.Sprintf("http://127.0.0.1:%d/mcp", v.Port), Error: s.lastError, Tokens: s.store.List(), OAuthWarning: s.store.RevocationWarning()}
}

// Configure binds before persisting success. On failure the previous listener
// and settings survive. Disabling closes active connections as well as accept.
func (s *Service) Configure(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("MCP service is closed")
	}
	if v.Enabled && s.startupError != nil {
		return s.startupError
	}
	if v.Port < 1024 || v.Port > 65535 {
		return errors.New("port must be 1024..65535")
	}
	if s.server != nil && v.Enabled && s.port == v.Port {
		return s.persistSettings(v)
	}
	var next *http.Server
	var listener net.Listener
	if v.Enabled {
		var err error
		listener, err = net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(v.Port)))
		if err != nil {
			s.lastError = "MCP loopback port is unavailable"
			return fmt.Errorf("bind MCP loopback port: %w", err)
		}
		next = &http.Server{Handler: s.Handler(v.Port), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Minute, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}
	}
	if err := s.persistSettings(v); err != nil {
		if listener != nil {
			_ = listener.Close()
		}
		return err
	}
	old := s.server
	oldListener := s.listener
	s.listener = listener
	s.server = next
	s.port = v.Port
	s.lastError = ""
	if oldListener != nil {
		_ = oldListener.Close()
	}
	if old != nil {
		_ = old.Close()
	}
	if !v.Enabled {
		s.closeArtifacts()
	} else {
		s.artifacts.mu.Lock()
		s.artifacts.closed = false
		s.artifacts.mu.Unlock()
	}
	if next != nil {
		go func() { _ = next.Serve(listener) }()
	}
	return nil
}

// Start restores only an explicitly persisted enable choice.
func (s *Service) Start() error {
	v := s.store.Settings()
	if !v.Enabled {
		return nil
	}
	return s.Configure(v)
}

// Close synchronously closes the listener, active requests and temporary artifacts.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.requestMu.Lock()
	s.requestCancel()
	s.requestMu.Unlock()
	s.closeListener()
	s.closeArtifacts()
	if s.retryCancel != nil {
		s.retryCancel()
		<-s.retryDone
		s.retryCancel = nil
		s.retryDone = nil
	}
	s.requests.Wait()
	if err := s.store.Close(); err != nil {
		s.lastError = "MCP storage close failed"
		slog.Error("MCP storage close failed", "err", err)
	}
}

// Handler serves only MCP and token-bound artifact routes.
//
//nolint:gocyclo // Authentication and network boundaries remain visible at one transport entry point.
func (s *Service) Handler(port int) http.Handler {
	servers := map[Role]*mcp.Server{}
	for _, role := range []Role{Observer, Developer} {
		server := mcp.NewServer(&mcp.Implementation{Name: "citeck-launcher", Version: "1"}, &mcp.ServerOptions{Instructions: serverInstructions(role)})
		for _, definition := range tools {
			if definition.developer && role != Developer {
				continue
			}
			mcp.AddTool(server, &definition.tool, func(ctx context.Context, _ *mcp.CallToolRequest, args Arguments) (*mcp.CallToolResult, any, error) {
				callCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				if requestCtx, ok := ctx.Value(requestContextKey{}).(context.Context); ok {
					stop := context.AfterFunc(requestCtx, cancel)
					defer stop()
					if requestCtx.Err() != nil {
						cancel()
					}
				}
				ctx = callCtx
				value, _ := ctx.Value(credentialKey{}).(string)
				token, ok := s.store.authenticatePrincipal(value)
				if !ok || definition.developer && token.Role != Developer {
					return nil, nil, errors.New("MCP authorization refused")
				}
				result, err := s.backend.Call(context.WithValue(ctx, issuerKey{}, token.artifactIdentity()), definition.tool.Name, args)
				outcome := "completed"
				if verdict, ok := result.(map[string]any); ok {
					if accepted, _ := verdict["accepted"].(bool); accepted {
						outcome = "accepted"
					}
					if state, _ := verdict["outcome"].(string); state == "unknown" || state == "completed" || state == "accepted" {
						outcome = state
					}
				}
				if err != nil {
					outcome = "refused"
				}
				slog.Info("MCP tool", "credentialKind", token.Kind, "credentialId", token.ID, "operation", definition.tool.Name, "workspaceId", args.WorkspaceID, "namespaceId", args.NamespaceID, "outcome", outcome)
				if err != nil {
					var refusal *ToolError
					if errors.As(err, &refusal) {
						b, _ := json.Marshal(refusal)
						return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
					}
					return nil, nil, fmt.Errorf("server: %w", err)
				}
				b, err := json.Marshal(result)
				if err != nil {
					return nil, nil, fmt.Errorf("server: %w", err)
				}
				if len(b) > MaxBytes {
					return nil, nil, errors.New("MCP result too large; narrow the request")
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
			})
		}
		servers[role] = server
	}
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		value, _ := r.Context().Value(credentialKey{}).(string)
		token, ok := s.store.authenticatePrincipal(value)
		if !ok {
			return nil
		}
		return servers[token.Role]
	}, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, PropagateRequestCancellation: true,
		// The outer handler below enforces the exact Host/port allowlist and
		// rejects every browser Origin before reaching the SDK. Its default
		// localhost-only gate would reject our explicit host.lima.internal
		// manual-token compatibility path on a real loopback listener.
		DisableLocalhostProtection: true,
	})
	slots := make(chan struct{}, 8)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requestMu.Lock()
		if s.requestContext.Err() != nil {
			s.requestMu.Unlock()
			http.Error(w, "MCP closed", http.StatusServiceUnavailable)
			return
		}
		s.requests.Add(1)
		s.requestMu.Unlock()
		defer s.requests.Done()
		requestCtx, requestCancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(s.requestContext, requestCancel)
		defer func() { stop(); requestCancel() }()
		r = r.WithContext(requestCtx)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		host, p, err := net.SplitHostPort(r.Host)
		if err != nil || p != strconv.Itoa(port) || (host != "127.0.0.1" && host != "localhost" && host != "::1" && host != "host.lima.internal") {
			http.Error(w, "MCP Host refused", http.StatusForbidden)
			return
		}
		if s.serveOAuth(w, r, port, host) {
			return
		}
		artifactPath := strings.HasPrefix(r.URL.Path, "/artifacts/")
		if (r.URL.Path != "/mcp" && !artifactPath) || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if len(r.Header.Values("Origin")) > 0 {
			http.Error(w, "browser origins are not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+canonicalIssuer(port)+`/.well-known/oauth-protected-resource/mcp", scope="mcp"`)
		auth := r.Header.Values("Authorization")
		if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") {
			http.Error(w, "Bearer token required", http.StatusUnauthorized)
			return
		}
		value := strings.TrimPrefix(auth[0], "Bearer ")
		if !strings.HasPrefix(value, "citeck_mcp_") && (host != "127.0.0.1" || s.store.Settings().Port != port) {
			http.Error(w, "OAuth requires the canonical MCP URL: "+canonicalResource(port), http.StatusUnauthorized)
			return
		}
		token, ok := s.store.authenticatePrincipal(value)
		if !ok {
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}
		w.Header().Del("WWW-Authenticate")
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "MCP busy", http.StatusTooManyRequests)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), credentialKey{}, value), 30*time.Minute)
		defer cancel()
		if artifactPath {
			s.downloadArtifact(w, r.WithContext(ctx), token)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBytes)
		h.ServeHTTP(w, r.WithContext(context.WithValue(ctx, requestContextKey{}, ctx)))
	})
}

// ToolError preserves native structured refusals across the MCP boundary.
type ToolError struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ToolError) Error() string { return e.Message }

// Caller holds the service mutex.
func (s *Service) closeListener() {
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
	if s.server != nil {
		_ = s.server.Close()
		s.server = nil
	}
}

func (s *Service) persistSettings(v Settings) error {
	err := s.store.SetSettings(v)
	if err != nil {
		if errors.Is(err, errMCPUncertain) {
			s.closeListener()
		}
		s.lastError = "MCP settings could not be saved"
	} else {
		s.lastError = ""
	}
	return err
}
