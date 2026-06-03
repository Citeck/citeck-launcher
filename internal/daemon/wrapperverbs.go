package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// wrapperClient calls native verbs on the desktop wrapper's control socket.
// In server mode the socket path is empty and calls are no-ops.
type wrapperClient struct {
	sockPath string
	http     *http.Client
}

func newWrapperClient(sockPath string) *wrapperClient {
	return &wrapperClient{
		sockPath: sockPath,
		http: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", sockPath)
				},
			},
		},
	}
}

// call POSTs /verb/<name> with optional JSON params. Returns nil if no wrapper
// socket is configured (server mode) — verbs are desktop-only best-effort.
func (c *wrapperClient) call(ctx context.Context, verb string, params any) error {
	if c == nil || c.sockPath == "" {
		return nil
	}
	var body bytes.Buffer
	if params != nil {
		if err := json.NewEncoder(&body).Encode(params); err != nil {
			return fmt.Errorf("encode verb params: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://wrapper/verb/"+verb, &body)
	if err != nil {
		return fmt.Errorf("build verb request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call wrapper verb %s: %w", verb, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wrapper verb %s returned %d", verb, resp.StatusCode)
	}
	return nil
}
