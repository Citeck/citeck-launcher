//go:build desktop

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails event names bridged to the webview. In desktop mode the web store
// subscribes to these instead of opening an EventSource: the Wails asset server
// on Windows (WebView2) buffers streaming HTTP responses, so SSE never delivers
// frames incrementally there. Native Wails events are plain webview IPC (not
// HTTP), so they stream identically on WebView2 and WebKitGTK — real-time on
// every platform.
const (
	wailsDaemonEvent  = "daemon:event"
	wailsDaemonPing   = "daemon:ping"
	wailsDaemonResync = "daemon:resync"
)

// streamDaemonEventsToWebview subscribes to the daemon's SSE event stream over
// the unix socket and re-emits each frame to the webview as a native Wails
// event. Runs for the life of the app, reconnecting with backoff. Emits a
// resync on every (re)connect so the frontend refetches and closes any gap that
// opened while disconnected.
func streamDaemonEventsToWebview(ctx context.Context, app *application.App, client *http.Client, daemonReady <-chan struct{}) {
	select {
	case <-daemonReady:
	case <-ctx.Done():
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		forwarded := streamDaemonEventsOnce(ctx, app, client)
		if ctx.Err() != nil {
			return
		}
		if forwarded > 0 {
			backoff = time.Second // a productive connection resets the backoff
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

// streamDaemonEventsOnce opens one SSE connection to the daemon and pumps frames
// to the webview until it drops. Returns the number of frames forwarded (used to
// reset the reconnect backoff after a productive connection).
func streamDaemonEventsOnce(ctx context.Context, app *application.App, client *http.Client) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/events", http.NoBody)
	if err != nil {
		return 0
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		slog.Debug("Event bridge: connect failed", "err", err)
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	// Fresh connection — tell the frontend to refetch so anything missed while
	// disconnected is reconciled.
	app.Event.Emit(wailsDaemonResync)

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var evType string
	var data strings.Builder
	forwarded := 0
	dispatch := func() {
		raw := data.String()
		evt := evType
		evType = ""
		data.Reset()
		switch evt {
		case "", "message":
			if raw == "" {
				return
			}
			app.Event.Emit(wailsDaemonEvent, json.RawMessage(raw))
		case "ping":
			app.Event.Emit(wailsDaemonPing)
		case "resync":
			app.Event.Emit(wailsDaemonResync)
		default:
			return
		}
		forwarded++
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			dispatch()
		case strings.HasPrefix(line, ":"):
			// comment / keepalive — ignore
		case strings.HasPrefix(line, "event:"):
			evType = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(line[len("data:"):]))
			// id: / retry: are irrelevant to the bridge (resync drives catch-up).
		}
	}
	return forwarded
}
