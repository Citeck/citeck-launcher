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

	"github.com/citeck/citeck-launcher/internal/namespace"
)

// CloudConfigServer serves Spring Cloud Config responses on port 8761.
// Apps connect to http://localhost:8761/config/{appName}/{profiles} to get
// their configuration, enabling the "stop in launcher, debug locally" workflow.
type CloudConfigServer struct {
	mu          sync.RWMutex
	cloudConfig map[string]map[string]any // per-app ext cloud config
	version     int64                     // monotonically increasing version
	server      *http.Server
}

// NewCloudConfigServer creates a new CloudConfigServer.
func NewCloudConfigServer() *CloudConfigServer {
	return &CloudConfigServer{}
}

// UpdateConfig replaces the cloud config data (called after regeneration).
func (s *CloudConfigServer) UpdateConfig(config map[string]map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cloudConfig = config
	s.version++
}

// Start begins serving on port 8761.
func (s *CloudConfigServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /config/{appName}", s.handleConfig)
	mux.HandleFunc("GET /config/{appName}/{profiles}", s.handleConfig)
	mux.HandleFunc("GET /config/{appName}/{profiles}/{rest...}", s.handleConfig)

	s.server = &http.Server{
		Handler:     mux,
		ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	listener, err := net.Listen("tcp", "127.0.0.1:8761")
	if err != nil {
		return fmt.Errorf("cloud config server listen: %w", err)
	}

	go func() {
		slog.Info("CloudConfigServer started", "addr", "127.0.0.1:8761")
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("CloudConfigServer error", "err", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the server.
func (s *CloudConfigServer) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(ctx)
	}
}

// handleConfig serves Spring Cloud Config JSON responses.
// Route: GET /config/{appName}/{profiles?}/{...}
func (s *CloudConfigServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	appName := r.PathValue("appName")
	profilesStr := r.PathValue("profiles")

	var profiles []string
	if profilesStr != "" {
		for _, p := range strings.Split(profilesStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				profiles = append(profiles, p)
			}
		}
	}

	// Base property source: JWT secret (always present)
	baseSrc := map[string]any{
		"ecos.webapp.web.authenticators.jwt.secret": namespace.JWTSecret(),
		"configserver.status":                       "Citeck Launcher Config Server",
	}
	propertySources := []propertySource{
		{Name: "citeck-launcher://application.yml", Source: baseSrc},
	}

	// Per-app property source from cloud config
	s.mu.RLock()
	appConfig := s.cloudConfig[appName]
	version := s.version
	s.mu.RUnlock()

	if len(appConfig) > 0 {
		propertySources = append(propertySources, propertySource{
			Name:   fmt.Sprintf("citeck-launcher://%s.yml", appName),
			Source: appConfig,
		})
	}

	resp := configResponse{
		Name:             appName,
		Profiles:         profiles,
		Label:            "main",
		Version:          fmt.Sprintf("%d", version),
		PropertySources:  propertySources,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
