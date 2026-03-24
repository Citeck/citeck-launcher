package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/niceteck/citeck-launcher/internal/api"
)

type DaemonClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

// New creates a client; returns error if daemon is not reachable.
func New(host, token string) (*DaemonClient, error) {
	tc, err := DetectTransport(host, token)
	if err != nil {
		return nil, err
	}
	return &DaemonClient{
		httpClient: NewHTTPClient(tc),
		baseURL:    tc.BaseURL(),
		token:      tc.Token,
	}, nil
}

// TryNew creates a client; returns nil if daemon is not reachable.
func TryNew(host, token string) *DaemonClient {
	c, err := New(host, token)
	if err != nil {
		return nil
	}
	return c
}

func (c *DaemonClient) Close() {
	c.httpClient.CloseIdleConnections()
}

func (c *DaemonClient) get(path string, result any) error {
	resp, err := c.doRequest("GET", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, result)
}

func (c *DaemonClient) getRaw(path string) (string, error) {
	resp, err := c.doRequest("GET", path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

func (c *DaemonClient) post(path string, body any, result any) error {
	resp, err := c.doRequest("POST", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, result)
}

func (c *DaemonClient) doRequest(method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	return c.httpClient.Do(req)
}

func decodeResponse(resp *http.Response, result any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errDto api.ErrorDto
		if json.Unmarshal(body, &errDto) == nil && errDto.Message != "" {
			return fmt.Errorf("%s", errDto.Message)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	if result != nil && len(body) > 0 {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// High-level API methods

func (c *DaemonClient) GetStatus() (*api.DaemonStatusDto, error) {
	var dto api.DaemonStatusDto
	err := c.get(api.DaemonStatus, &dto)
	return &dto, err
}

func (c *DaemonClient) IsRunning() bool {
	status, err := c.GetStatus()
	return err == nil && status.Running
}

func (c *DaemonClient) Shutdown() (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.DaemonShutdown, nil, &dto)
	return &dto, err
}

func (c *DaemonClient) GetNamespace() (*api.NamespaceDto, error) {
	var dto api.NamespaceDto
	err := c.get(api.Namespace, &dto)
	return &dto, err
}

func (c *DaemonClient) StartNamespace() (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.NamespaceStart, nil, &dto)
	return &dto, err
}

func (c *DaemonClient) StopNamespace() (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.NamespaceStop, nil, &dto)
	return &dto, err
}

func (c *DaemonClient) ReloadNamespace() (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.NamespaceReload, nil, &dto)
	return &dto, err
}

func (c *DaemonClient) GetAppLogs(name string, tail int) (string, error) {
	path := fmt.Sprintf("%s?tail=%d", api.AppLogs(name), tail)
	return c.getRaw(path)
}

func (c *DaemonClient) RestartApp(name string) (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.AppRestart(name), nil, &dto)
	return &dto, err
}

func (c *DaemonClient) InspectApp(name string) (*api.AppInspectDto, error) {
	var dto api.AppInspectDto
	err := c.get(api.AppInspect(name), &dto)
	return &dto, err
}

func (c *DaemonClient) ExecApp(name string, command []string) (*api.ExecResultDto, error) {
	var dto api.ExecResultDto
	err := c.post(api.AppExec(name), api.ExecRequestDto{Command: command}, &dto)
	return &dto, err
}

func (c *DaemonClient) GetHealth() (*api.HealthDto, error) {
	var dto api.HealthDto
	err := c.get(api.Health, &dto)
	return &dto, err
}
