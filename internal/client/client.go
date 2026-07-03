package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
)

// DaemonClient communicates with a running Citeck daemon over Unix socket or TCP.
type DaemonClient struct {
	httpClient   *http.Client
	streamClient *http.Client // no timeout, for streaming (log follow, SSE)
	baseURL      string
}

// Options holds options for creating a DaemonClient.
type Options struct {
	Host       string
	TLSCert    string
	TLSKey     string
	ServerCert string
	Insecure   bool
}

// New creates a client; returns error if daemon is not reachable or TLS config is invalid.
func New(opts Options) (*DaemonClient, error) {
	tc, err := DetectTransport(opts.Host, opts.TLSCert, opts.TLSKey, opts.ServerCert, opts.Insecure)
	if err != nil {
		return nil, err
	}
	// Validate TLS config eagerly so misconfiguration is reported clearly
	if tc.useTLS() {
		if _, err := tc.buildTLSConfig(); err != nil {
			return nil, fmt.Errorf("TLS configuration error: %w", err)
		}
	}
	return &DaemonClient{
		httpClient:   NewHTTPClient(tc),
		streamClient: NewStreamingHTTPClient(tc),
		baseURL:      tc.BaseURL(),
	}, nil
}

// TryNew creates a client; returns nil if daemon is not reachable.
func TryNew(opts Options) *DaemonClient {
	c, err := New(opts)
	if err != nil {
		return nil
	}
	return c
}

// Close releases idle connections held by the underlying HTTP client.
func (c *DaemonClient) Close() {
	c.httpClient.CloseIdleConnections()
}

func (c *DaemonClient) get(path string, result any) error {
	resp, err := c.doRequest(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, result)
}

func (c *DaemonClient) getRaw(path string) (string, error) {
	resp, err := c.doRequest(http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

func (c *DaemonClient) post(path string, body, result any) error {
	resp, err := c.doRequest(http.MethodPost, path, body)
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
		return nil, fmt.Errorf("create %s request: %w", method, err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, doErr := c.httpClient.Do(req)
	if doErr != nil {
		return nil, fmt.Errorf("execute %s %s: %w", method, path, doErr)
	}
	return resp, nil
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

// GetStatus retrieves the daemon's current status.
func (c *DaemonClient) GetStatus() (*api.DaemonStatusDto, error) {
	var dto api.DaemonStatusDto
	err := c.get(api.DaemonStatus, &dto)
	return &dto, err
}

// IsRunning reports whether the daemon is reachable and running.
func (c *DaemonClient) IsRunning() bool {
	status, err := c.GetStatus()
	return err == nil && status.Running
}

// Shutdown requests a graceful daemon shutdown (containers stopped).
func (c *DaemonClient) Shutdown() (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.DaemonShutdown, nil, &dto)
	return &dto, err
}

// ShutdownLeaveRunning asks the daemon to exit without stopping containers.
// Used for binary upgrades — the next daemon adopts the running platform
// via doStart's hash-matching path.
func (c *DaemonClient) ShutdownLeaveRunning() (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.DaemonShutdown+"?leave_running=true", nil, &dto)
	return &dto, err
}

// GetNamespace retrieves the current namespace configuration and state.
func (c *DaemonClient) GetNamespace() (*api.NamespaceDto, error) {
	var dto api.NamespaceDto
	err := c.get(api.Namespace, &dto)
	return &dto, err
}

// StartNamespace starts the active namespace's containers.
func (c *DaemonClient) StartNamespace() (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.NamespaceStart, nil, &dto)
	return &dto, err
}

// StopNamespace stops the active namespace's containers.
func (c *DaemonClient) StopNamespace() (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.NamespaceStop, nil, &dto)
	return &dto, err
}

// ReloadNamespace re-reads the config and reconciles the namespace.
func (c *DaemonClient) ReloadNamespace() (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.NamespaceReload, nil, &dto)
	return &dto, err
}

// ReloadPlan returns the per-app plan of what a reload would do right now
// (dry-run): create / recreate / keep / remove / detached verdicts plus
// hash-input line diffs for recreated apps.
func (c *DaemonClient) ReloadPlan() (*api.ReloadPlanDto, error) {
	var dto api.ReloadPlanDto
	err := c.get(api.NamespaceReloadPlan, &dto)
	return &dto, err
}

// SetAdminPassword resets the ecos-app realm admin password by driving
// kcadm.sh inside the running keycloak container via the daemon.
// The new password is also stored in the encrypted secret store so the
// value the daemon knows about stays consistent with what keycloak holds.
func (c *DaemonClient) SetAdminPassword(password string) (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.NamespaceAdminPassword, map[string]string{"password": password}, &dto)
	return &dto, err
}

// GetAppLogs retrieves container logs for the named application.
func (c *DaemonClient) GetAppLogs(name string, tail int, since, until string, timestamps bool) (string, error) {
	params := url.Values{"tail": {strconv.Itoa(tail)}}
	if since != "" {
		params.Set("since", since)
	}
	if until != "" {
		params.Set("until", until)
	}
	if timestamps {
		params.Set("timestamps", "true")
	}
	path := api.AppLogs(name) + "?" + params.Encode()
	return c.getRaw(path)
}

// StreamAppLogs returns a streaming reader for container logs (follow mode).
// The caller must close the returned ReadCloser.
func (c *DaemonClient) StreamAppLogs(name string, tail int) (io.ReadCloser, error) {
	logsURL := c.baseURL + fmt.Sprintf("%s?tail=%d&follow=true", api.AppLogs(name), tail)
	req, err := http.NewRequest(http.MethodGet, logsURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create log stream request: %w", err)
	}

	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to log stream: %w", err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
}

// GetDaemonLogs returns daemon log lines.
func (c *DaemonClient) GetDaemonLogs(tail int) (string, error) {
	path := fmt.Sprintf("%s?tail=%d", api.DaemonLogs, tail)
	return c.getRaw(path)
}

// StreamDaemonLogs returns a streaming reader for daemon logs (follow mode).
func (c *DaemonClient) StreamDaemonLogs(tail int) (io.ReadCloser, error) {
	logsURL := c.baseURL + fmt.Sprintf("%s?tail=%d&follow=true", api.DaemonLogs, tail)
	req, err := http.NewRequest(http.MethodGet, logsURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create daemon log stream request: %w", err)
	}

	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon log stream: %w", err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
}

// ListSnapshots returns available volume snapshots.
func (c *DaemonClient) ListSnapshots() ([]api.SnapshotDto, error) {
	var snapshots []api.SnapshotDto
	err := c.get(api.Snapshots, &snapshots)
	return snapshots, err
}

// ExportSnapshot triggers a volume snapshot export.
// If outputDir is non-empty, the snapshot is written there instead of the default directory.
func (c *DaemonClient) ExportSnapshot(outputDir string) (*api.ActionResultDto, error) {
	path := api.SnapshotsExport
	if outputDir != "" {
		path += "?output=" + url.QueryEscape(outputDir)
	}
	var dto api.ActionResultDto
	err := c.post(path, nil, &dto)
	return &dto, err
}

// ImportSnapshot imports a named snapshot into the namespace volumes.
func (c *DaemonClient) ImportSnapshot(name string) (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.SnapshotsImport+"?name="+url.QueryEscape(name), nil, &dto)
	return &dto, err
}

// UpgradeNamespace changes the bundle version and triggers a reload.
func (c *DaemonClient) UpgradeNamespace(bundleRef string) (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.NamespaceUpgrade, api.UpgradeRequestDto{BundleRef: bundleRef}, &dto)
	return &dto, err
}

// SaveSecret creates or updates a secret via the daemon API.
func (c *DaemonClient) SaveSecret(id, value string) error {
	return c.post(api.Secrets, api.SecretCreateDto{ID: id, Value: value}, nil)
}

// RestartApp restarts the named application container.
func (c *DaemonClient) RestartApp(name string) (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.AppRestart(name), nil, &dto)
	return &dto, err
}

// StopApp stops a single application.
func (c *DaemonClient) StopApp(name string) (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.AppStop(name), nil, &dto)
	return &dto, err
}

// StartApp starts a single application.
func (c *DaemonClient) StartApp(name string) (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	err := c.post(api.AppStart(name), nil, &dto)
	return &dto, err
}

// InspectApp returns detailed info about a running application container.
func (c *DaemonClient) InspectApp(name string) (*api.AppInspectDto, error) {
	var dto api.AppInspectDto
	err := c.get(api.AppInspect(name), &dto)
	return &dto, err
}

// ExecApp runs a command inside the named application's container.
func (c *DaemonClient) ExecApp(name string, command []string) (*api.ExecResultDto, error) {
	var dto api.ExecResultDto
	err := c.post(api.AppExec(name), api.ExecRequestDto{Command: command}, &dto)
	return &dto, err
}

// GetHealth returns the aggregate health status of all applications.
func (c *DaemonClient) GetHealth() (*api.HealthDto, error) {
	var dto api.HealthDto
	err := c.get(api.Health, &dto)
	return &dto, err
}

// GetLicenseStatus fetches the effective enterprise-license summary.
// Returns (nil, nil) when the daemon predates the endpoint (404) so callers
// can silently omit license info when talking to an older daemon.
func (c *DaemonClient) GetLicenseStatus() (*api.LicenseStatusDto, error) {
	resp, err := c.doRequest(http.MethodGet, api.LicensesStatus, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	var dto api.LicenseStatusDto
	if err := decodeResponse(resp, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

// GetAppliedConfig returns the config currently driving the running namespace
// (the config snapshot from when containers were last generated).
func (c *DaemonClient) GetAppliedConfig() (string, error) {
	return c.getRaw(api.ConfigApplied)
}

// StreamEvents opens an SSE connection to the daemon and sends events to the channel.
// Blocks until the context is canceled. The channel is closed when the function returns.
func (c *DaemonClient) StreamEvents(ctx context.Context) (<-chan api.EventDto, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+api.Events, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create event stream request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to event stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("event stream: HTTP %d", resp.StatusCode)
	}

	ch := make(chan api.EventDto, 64)
	go func() {
		defer resp.Body.Close()
		defer close(ch)

		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				// Parse complete SSE messages (terminated by "\n\n")
				for {
					idx := bytes.Index(buf, []byte("\n\n"))
					if idx < 0 {
						break
					}
					msg := string(buf[:idx])
					buf = buf[idx+2:]

					// Extract "data: ..." line
					for line := range strings.SplitSeq(msg, "\n") {
						if strings.HasPrefix(line, "data: ") {
							payload := line[6:]
							var evt api.EventDto
							if json.Unmarshal([]byte(payload), &evt) == nil {
								select {
								case ch <- evt:
								case <-ctx.Done():
									return
								}
							}
						}
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	return ch, nil
}

// APIError is returned when the daemon responds with an HTTP error status.
// Callers inspect Status to decide whether an error is retryable — e.g. a 400
// from an invalid edit re-opens the editor rather than aborting.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// putRaw sends body verbatim (no JSON marshaling) with the given content type.
// Used for the app-config editor, whose PUT body is raw YAML.
func (c *DaemonClient) putRaw(path, contentType string, body []byte, result any) error {
	req, err := http.NewRequest(http.MethodPut, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create PUT request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)

	resp, doErr := c.httpClient.Do(req)
	if doErr != nil {
		return fmt.Errorf("execute PUT %s: %w", path, doErr)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		msg := string(respBody)
		var errDto api.ErrorDto
		if json.Unmarshal(respBody, &errDto) == nil && errDto.Message != "" {
			msg = errDto.Message
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// GetAppConfig returns the app's effective ApplicationDef YAML plus the
// generated baseline.
func (c *DaemonClient) GetAppConfig(name string) (*api.AppConfigDto, error) {
	var dto api.AppConfigDto
	if err := c.get(api.Apps+"/"+url.PathEscape(name)+"/config", &dto); err != nil {
		return nil, fmt.Errorf("get app config: %w", err)
	}
	return &dto, nil
}

// PutAppConfig saves an edited ApplicationDef (raw YAML). On an HTTP error the
// returned error wraps *APIError so a 400 (invalid YAML) can be told apart.
func (c *DaemonClient) PutAppConfig(name string, body []byte) (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	if err := c.putRaw(api.Apps+"/"+url.PathEscape(name)+"/config", "text/yaml", body, &dto); err != nil {
		return nil, fmt.Errorf("put app config: %w", err)
	}
	return &dto, nil
}

// ResetAppConfig clears the app's config override, restoring the generated def.
func (c *DaemonClient) ResetAppConfig(name string) (*api.ActionResultDto, error) {
	var dto api.ActionResultDto
	if err := c.post(api.Apps+"/"+url.PathEscape(name)+"/config/reset", nil, &dto); err != nil {
		return nil, fmt.Errorf("reset app config: %w", err)
	}
	return &dto, nil
}
