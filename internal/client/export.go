package client

import (
	"fmt"
	"io"
	"net/http"

	"github.com/citeck/citeck-launcher/internal/api"
)

// The export directory: an app's outbound artifacts (heap dumps, pg_dumps,
// thread dumps) that live on the DAEMON's host and have to reach the caller's.

// ListAppExports returns what is in an app's export directory, newest first.
func (c *DaemonClient) ListAppExports(name string) ([]api.ExportFileDto, error) {
	var files []api.ExportFileDto
	if err := c.get(api.AppExport(name), &files); err != nil {
		return nil, fmt.Errorf("list exports of %q: %w", name, err)
	}
	return files, nil
}

// StreamAppExport opens one export file for reading. The caller closes it.
//
// The streaming client is used rather than the plain one because these files
// are as large as whatever produced them — a heap dump is the size of the heap
// — and the ordinary request timeout would cut a slow transfer off midway.
func (c *DaemonClient) StreamAppExport(name, file string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+api.AppExportFile(name, file), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create export download request: %w", err)
	}
	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download export %q of %q: %w", file, name, err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
}

// DeleteAppExport removes one file from an app's export directory.
func (c *DaemonClient) DeleteAppExport(name, file string) error {
	resp, err := c.doRequest(http.MethodDelete, api.AppExportFile(name, file), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeResponse(resp, nil)
}
