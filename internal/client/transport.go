package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/niceteck/citeck-launcher/internal/config"
)

type TransportType int

const (
	TransportUnix TransportType = iota
	TransportTCP
)

type TransportConfig struct {
	Type       TransportType
	SocketPath string
	Host       string
	Token      string
}

func DetectTransport(host, token string) (*TransportConfig, error) {
	if host != "" {
		return &TransportConfig{
			Type:  TransportTCP,
			Host:  host,
			Token: token,
		}, nil
	}

	if envHost := os.Getenv("CITECK_HOST"); envHost != "" {
		if token == "" {
			token = os.Getenv("CITECK_TOKEN")
		}
		return &TransportConfig{
			Type:  TransportTCP,
			Host:  envHost,
			Token: token,
		}, nil
	}

	socketPath := config.SocketPath()
	if _, err := os.Stat(socketPath); err == nil {
		return &TransportConfig{
			Type:       TransportUnix,
			SocketPath: socketPath,
		}, nil
	}

	return nil, fmt.Errorf("daemon is not running (no socket at %s)", socketPath)
}

func NewHTTPClient(tc *TransportConfig) *http.Client {
	switch tc.Type {
	case TransportUnix:
		return &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.DialTimeout("unix", tc.SocketPath, 5*time.Second)
				},
			},
		}
	case TransportTCP:
		return &http.Client{
			Timeout: 30 * time.Second,
		}
	default:
		return http.DefaultClient
	}
}

func (tc *TransportConfig) BaseURL() string {
	switch tc.Type {
	case TransportUnix:
		return "http://localhost"
	case TransportTCP:
		return "http://" + tc.Host
	default:
		return "http://localhost"
	}
}
