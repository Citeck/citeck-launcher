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
// HTTP), so they stream identically on WebView2, WKWebView and WebKitGTK.
//
// That IPC uses ExecJS, which Wails queues until the window is runtime-ready.
// MarkRuntimeReadyOnFirstLoad releases it for every window this wrapper creates,
// including pages without a frontend handshake. The v2.17.3 frontend also marks
// readiness itself after subscribing; loading the full Wails JS runtime is not
// required. See docs/desktop-webview.md for the version boundary.
const (
	wailsDaemonEvent      = "daemon:event"
	wailsDaemonPing       = "daemon:ping"
	wailsDaemonResync     = "daemon:resync"
	wailsDaemonDisconnect = "daemon:disconnect"
)

// streamDaemonEventsToWebview subscribes to the daemon's SSE event stream over
// the unix socket and re-emits each frame to the webview as a native Wails
// event. Runs for the life of the app, reconnecting with backoff and replaying
// from the last complete SSE frame. Refetch alone cannot recover a missed
// snapshot completion because it is not part of the namespace DTO.
func streamDaemonEventsToWebview(ctx context.Context, app *application.App, client *http.Client, daemonReady <-chan struct{}) {
	select {
	case <-daemonReady:
	case <-ctx.Done():
		return
	}
	backoff := time.Second
	lastEventID := ""
	for ctx.Err() == nil {
		forwarded := streamDaemonEventsOnce(ctx, func(name string, data ...any) bool {
			// Event.Emit dispatches each event to windows in a separate goroutine
			// in Wails alpha.98. That reorders a burst of sequential SSE frames.
			// Dispatch on this stream goroutine so every window keeps SSE order,
			// including the resync/disconnect markers around a connection.
			event := &application.CustomEvent{Name: name}
			if len(data) == 1 {
				event.Data = data[0]
			} else if len(data) > 1 {
				event.Data = data
			}
			for _, window := range app.Window.GetAll() {
				window.DispatchWailsEvent(event)
			}
			return false
		}, client, &lastEventID)
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
func streamDaemonEventsOnce(ctx context.Context, emit func(string, ...any) bool, client *http.Client, lastEventID *string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/events", http.NoBody)
	if err != nil {
		return 0
	}
	req.Header.Set("Accept", "text/event-stream")
	if *lastEventID != "" {
		req.Header.Set("Last-Event-ID", *lastEventID)
	}
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
	// disconnected is reconciled. The paired disconnect (on this connection
	// dropping) lets the frontend mark sseConnected=false so the long-op stall
	// watchdog fires on desktop just as it does for an EventSource onClose.
	emit(wailsDaemonResync)
	defer emit(wailsDaemonDisconnect)

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var evType string
	var pendingID string
	var data strings.Builder
	forwarded := 0
	dispatch := func() {
		raw := data.String()
		evt := evType
		id := pendingID
		pendingID = ""
		evType = ""
		data.Reset()
		switch evt {
		case "", "message":
			if raw == "" {
				return
			}
			emit(wailsDaemonEvent, json.RawMessage(raw))
			if id != "" {
				*lastEventID = id
			}
		case "ping":
			emit(wailsDaemonPing)
		case "resync":
			emit(wailsDaemonResync)
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
		case strings.HasPrefix(line, "id:"):
			pendingID = strings.TrimSpace(line[len("id:"):])
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(line[len("data:"):]))
			// retry: is advisory; the wrapper owns reconnect backoff.
		}
	}
	return forwarded
}
