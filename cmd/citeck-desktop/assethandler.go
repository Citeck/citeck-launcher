//go:build desktop

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/citeck/citeck-launcher/internal/desktop/wailswin"
)

// assetDeps bundles the shared state the Wails asset handler needs. windowMgr is
// a double pointer because the WindowManager is assigned after this handler is
// constructed (Wails dereferences it lazily, at request time, by which point it
// is wired).
type assetDeps struct {
	socketPath   string
	socketClient *http.Client
	daemonStatus *desktop.DaemonStatus
	daemonReady  <-chan struct{}
	windowMgr    **wailswin.WindowManager
	dumpInFlight *atomic.Bool
}

// newAssetHandler builds the loading/error + proxy asset handler. It is served
// until the daemon is ready, then proxies to the daemon over the unix socket.
// /desktop/windows/* and /desktop/system-dump are intercepted (Wails-only APIs).
func newAssetHandler(d assetDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/desktop/windows/") {
			wm := *d.windowMgr
			if wm == nil {
				http.Error(w, "window manager not ready", http.StatusServiceUnavailable)
				return
			}
			http.StripPrefix("/desktop/windows", wm.HTTPHandler()).ServeHTTP(w, r)
			return
		}
		// Native system dump for the web UI. The browser <a download> path the
		// web button used does nothing in the WebKitGTK webview (no download
		// handler), so the desktop UI routes here instead: write the ZIP to
		// disk and open the containing folder, exactly like the tray item and
		// Kotlin 1.x's SystemDumpUtils (Desktop.open(reportDir)). Returns the
		// saved path so the frontend can show it in the success toast.
		if r.URL.Path == "/desktop/system-dump" {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleDesktopSystemDump(w, d.socketPath, d.dumpInFlight)
			return
		}
		select {
		case <-d.daemonReady:
			proxyViaSocket(w, r, d.socketClient, d.daemonStatus)
		default:
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "daemon starting", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(errorPageHTML(d.daemonStatus, nil)))
		}
	}
}

// proxyViaSocket forwards the request to the daemon via the Unix socket
// manually. Wails AssetServer sends the body with ContentLength=0 (streamed),
// which breaks httputil.ReverseProxy; a direct HTTP client handles it correctly.
func proxyViaSocket(w http.ResponseWriter, r *http.Request, socketClient *http.Client, daemonStatus *desktop.DaemonStatus) {
	// Buffer body — Wails may stream it with ContentLength=0
	var bodyReader io.Reader = http.NoBody
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if len(bodyBytes) > 0 {
			bodyReader = bytes.NewReader(bodyBytes)
		}
	}

	targetURL := "http://localhost" + r.URL.RequestURI()
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bodyReader) //nolint:gosec // G704: proxy to local Unix socket, not user-controlled URL
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// Copy headers
	for k, vv := range r.Header {
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}

	resp, err := socketClient.Do(proxyReq) //nolint:gosec // G704: proxy to local Unix socket, not user-controlled URL
	if err != nil {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			msg := "daemon starting"
			if lastErr := daemonStatus.LastError(); lastErr != "" {
				msg = lastErr
			}
			http.Error(w, msg, http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(errorPageHTML(daemonStatus, err)))
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// Stream the (possibly SSE / chunked-log) response body to the webview.
	streamResponseBody(w, resp.Body)
}

// handleDesktopSystemDump runs the native system dump on behalf of the web UI
// button. The browser <a download> path the button used does nothing in the
// WebKitGTK webview (no download handler), so the desktop UI POSTs here: we
// write the ZIP to disk and open its folder, exactly like the tray item and
// Kotlin 1.x's SystemDumpUtils (Desktop.open(reportDir)), then return the saved
// path so the frontend can show it. dumpInFlight is shared with the tray item
// so the two can't export in parallel.
func handleDesktopSystemDump(w http.ResponseWriter, socketPath string, dumpInFlight *atomic.Bool) {
	if !dumpInFlight.CompareAndSwap(false, true) {
		http.Error(w, "dump already in progress", http.StatusConflict)
		return
	}
	defer dumpInFlight.Store(false)
	zipPath, err := dumpSystemInfo(socketPath)
	if err != nil {
		slog.Error("System dump failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("System dump created", "path", zipPath)
	if openErr := desktop.OpenBrowser("file://" + filepath.Dir(zipPath)); openErr != nil {
		slog.Warn("Failed to open dump folder", "err", openErr)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": zipPath})
}

// streamResponseBody copies a (possibly streaming) upstream response body to w,
// flushing after each chunk so SSE events and chunked logs arrive live. It stops
// on a write error: a broken pipe means the webview closed the request (the user
// switched the log tail or closed the window), so we must stop reading and let
// the caller's deferred Body.Close tear the upstream daemon stream down —
// otherwise the proxy goroutine and the daemon-side follow stream leak for the
// life of the process.
func streamResponseBody(w http.ResponseWriter, body io.Reader) {
	f, ok := w.(http.Flusher)
	if !ok {
		_, _ = io.Copy(w, body)
		return
	}
	buf := make([]byte, 4096)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			f.Flush()
		}
		if readErr != nil {
			return
		}
	}
}

// errorPageHTML generates an informative loading/error page with auto-refresh.
// On error: shows the error message and startup logs so the user (and developer) can see what happened.
// On startup: shows a spinner with "Starting...".
func errorPageHTML(status *desktop.DaemonStatus, proxyErr error) string {
	title := "Starting..."
	errMsg := ""
	logLines := ""

	if status != nil {
		if lastErr := status.LastError(); lastErr != "" {
			title = "Daemon failed to start"
			errMsg = lastErr
			if f := status.Failures(); f > 1 {
				title = fmt.Sprintf("Daemon failed to start (attempt %d)", f)
			}
			logLines = status.LogLines()
		}
	}
	if proxyErr != nil && errMsg == "" {
		errMsg = proxyErr.Error()
		title = "Connecting to daemon..."
	}

	errorBlock := ""
	if errMsg != "" {
		errorBlock = `<div class="error-box">` + errMsg + `</div>`
	}
	logBlock := ""
	if logLines != "" {
		logBlock = `<div class="log-title">Startup log:</div><pre class="log-box">` + logLines + `</pre>`
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta http-equiv="refresh" content="2"><style>
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
background:#1e1e1e;color:#888;font-family:system-ui,sans-serif;font-size:14px}
.loader{text-align:center;max-width:800px;width:100%%;padding:24px}
.spinner{width:28px;height:28px;border:3px solid #333;border-top:3px solid #888;
border-radius:50%%;animation:spin 1s linear infinite;margin:0 auto 12px}
@keyframes spin{to{transform:rotate(360deg)}}
h2{margin:0 0 8px;color:#ccc;font-size:16px}
.error-box{margin-top:12px;padding:10px 14px;background:#2a1a1a;border:1px solid #5c2020;
border-radius:6px;color:#ef5350;font-size:13px;text-align:left;word-break:break-word;font-family:monospace}
.log-title{margin-top:16px;font-size:12px;color:#666;text-align:left}
.log-box{margin-top:4px;padding:10px 14px;background:#161616;border:1px solid #333;
border-radius:6px;color:#999;font-size:11px;text-align:left;white-space:pre-wrap;word-break:break-all;
font-family:monospace;max-height:400px;overflow:auto;line-height:1.5}
</style></head><body><div class="loader"><div class="spinner"></div><h2>%s</h2>%s%s</div></body></html>`,
		title, errorBlock, logBlock)
}
