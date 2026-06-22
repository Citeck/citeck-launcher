package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CloudConfigServer serves Spring Cloud Config responses on port 8761.
// Apps connect to http://localhost:8761/config/{appName}/{profiles} to get
// their configuration, enabling the "stop in launcher, debug locally" workflow.
type CloudConfigServer struct {
	mu          sync.RWMutex
	cloudConfig map[string]map[string]any // per-app ext cloud config
	jwtSecret   string                    // JWT secret for base property source
	version     int64                     // monotonically increasing version

	lifecycleMu sync.Mutex   // guards started/server so Start/Stop are idempotent + reentrant
	started     bool         // true while the HTTP listener is bound
	server      *http.Server // the live server while started; nil otherwise
	addr        string       // listen address; overridable in tests (default cloudConfigAddr)
}

// cloudConfigAddr is the default bind address — see the SECURITY note on the
// 0.0.0.0 choice in Start.
const cloudConfigAddr = "0.0.0.0:8761"

// NewCloudConfigServer creates a new CloudConfigServer.
func NewCloudConfigServer() *CloudConfigServer {
	return &CloudConfigServer{addr: cloudConfigAddr}
}

// UpdateConfig replaces the cloud config data (called after regeneration).
func (s *CloudConfigServer) UpdateConfig(config map[string]map[string]any, jwtSecret string) {
	flat := make(map[string]map[string]any, len(config))
	for app, cfg := range config {
		out := make(map[string]any, len(cfg))
		flattenCloudConfig(out, cfg, "")
		flat[app] = out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cloudConfig = flat
	if jwtSecret != "" {
		s.jwtSecret = jwtSecret
	}
	s.version++
}

// flattenCloudConfig mirrors Kotlin CloudConfigImpl.buildFlattenedMap so that
// nested workspace cloudConfig maps bind correctly to Spring's dot-notation
// property keys (e.g. spring.datasource.url).
func flattenCloudConfig(result, source map[string]any, path string) {
	for srcKey, value := range source {
		key := srcKey
		if path != "" && strings.TrimSpace(path) != "" {
			if strings.HasPrefix(key, "[") {
				key = path + key
			} else {
				key = path + "." + key
			}
		}
		switch v := value.(type) {
		case string:
			result[key] = v
		case map[string]any:
			flattenCloudConfig(result, v, key)
		case map[any]any:
			converted := make(map[string]any, len(v))
			for k, val := range v {
				if ks, ok := k.(string); ok {
					converted[ks] = val
				}
			}
			flattenCloudConfig(result, converted, key)
		case []any:
			if len(v) == 0 {
				result[key] = ""
			} else {
				for idx, item := range v {
					flattenCloudConfig(result, map[string]any{fmt.Sprintf("[%d]", idx): item}, key)
				}
			}
		default:
			result[key] = v
		}
	}
}

// Start begins serving on port 8761. Idempotent: a no-op (returns nil) when the
// server is already running, so the namespace-status lifecycle hook can call it
// freely on every transition into a running state.
func (s *CloudConfigServer) Start() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /config/{appName}", s.handleConfig)
	mux.HandleFunc("GET /config/{appName}/{profiles}", s.handleConfig)
	mux.HandleFunc("GET /config/{appName}/{profiles}/{rest...}", s.handleConfig)

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Bind all interfaces (Kotlin 1.x parity — Ktor's CIO server defaulted to
	// 0.0.0.0:8761). Desktop mode runs this only for the local-debug workflow,
	// and external microservices run OUTSIDE docker (a host process, a sibling
	// container reaching the host via the bridge gateway, or a VM/WSL service)
	// must reach the config server to fetch host-published broker/zk/db
	// addresses. A loopback-only bind (127.0.0.1) refused every non-localhost
	// connection — the 2.x regression that broke external-service config.
	// SECURITY (reviewed, deliberate — operator-confirmed not sensitive):
	// automated review flags the all-interfaces bind HIGH because the response
	// carries a JWT secret + DB credentials. In DESKTOP mode those are NOT real
	// secrets: they are local-dev throwaway values (admin/admin-class creds, a
	// per-install JWT that only guards local dev webapps) — there is no
	// production data to leak. This server is desktop-only (server mode never
	// starts it) and 0.0.0.0 is required: loopback-only (the 2.x regression)
	// refused real external clients whose traffic does not arrive via loopback
	// (sibling container via the bridge gateway, VM/WSL), which worked under
	// Kotlin's 0.0.0.0 bind. If real secrets ever flow here, gate the bind
	// behind a daemon.yml option (127.0.0.1 to harden) or add a bearer token.
	addr := s.addr
	if addr == "" {
		addr = cloudConfigAddr
	}
	listener, err := net.Listen("tcp", addr) //nolint:gosec // G102: desktop-only local-debug config server serving non-sensitive dev values, Kotlin parity (see SECURITY note above)
	if err != nil {
		s.server = nil
		return fmt.Errorf("cloud config server listen: %w", err)
	}
	s.started = true

	srv := s.server
	go func() {
		slog.Info("CloudConfigServer started", "addr", addr)
		serveErr := srv.Serve(listener)
		if serveErr != nil && serveErr != http.ErrServerClosed {
			slog.Error("CloudConfigServer error", "err", serveErr)
		}
		// Serve returned ⇒ the port is released. Clear started so a later Start()
		// can rebind instead of no-op'ing on a dead server. Skip when Stop()
		// already tore this instance down (it nils s.server under the lock); the
		// `s.server == srv` guard distinguishes a Stop-driven shutdown from a
		// spontaneous Serve failure and ignores a stale goroutine after a
		// Stop→Start cycle replaced the server.
		s.lifecycleMu.Lock()
		if s.server == srv {
			s.started = false
			s.server = nil
		}
		s.lifecycleMu.Unlock()
	}()

	return nil
}

// Stop gracefully shuts down the server and releases port 8761. Idempotent: a
// no-op when not started, so the lifecycle hook can call it on every transition
// into STOPPED. Releasing the port matters — a still-bound :8761 from a stopped
// namespace makes the next daemon start fail with "address already in use".
func (s *CloudConfigServer) Stop() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.started {
		return
	}
	srv := s.server
	s.started = false
	s.server = nil
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("CloudConfigServer shutdown error", "err", err)
	}
}

// handleConfig serves Spring Cloud Config JSON responses.
// Route: GET /config/{appName}/{profiles?}/{...}
func (s *CloudConfigServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	appName := r.PathValue("appName")
	profilesStr := r.PathValue("profiles")

	var profiles []string
	if profilesStr != "" {
		for p := range strings.SplitSeq(profilesStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				profiles = append(profiles, p)
			}
		}
	}

	// Read all shared state under a single lock
	s.mu.RLock()
	jwt := s.jwtSecret
	appConfig := s.cloudConfig[appName]
	version := s.version
	s.mu.RUnlock()

	// Base property source: JWT secret (always present)
	baseSrc := map[string]any{
		"ecos.webapp.web.authenticators.jwt.secret": jwt,
		"configserver.status":                       "Citeck Launcher Config Server",
	}
	propertySources := []propertySource{
		{Name: "citeck-launcher://application.yml", Source: baseSrc},
	}

	if len(appConfig) > 0 {
		propertySources = append(propertySources, propertySource{
			Name:   fmt.Sprintf("citeck-launcher://%s.yml", appName),
			Source: appConfig,
		})
	}

	resp := configResponse{
		Name:            appName,
		Profiles:        profiles,
		Label:           "main",
		Version:         fmt.Sprintf("%d", version),
		PropertySources: propertySources,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type configResponse struct {
	Name            string           `json:"name"`
	Profiles        []string         `json:"profiles"`
	Label           string           `json:"label"`
	Version         string           `json:"version"`
	PropertySources []propertySource `json:"propertySources"`
}

type propertySource struct {
	Name   string         `json:"name"`
	Source map[string]any `json:"source"`
}
