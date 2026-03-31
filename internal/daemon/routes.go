package daemon

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/acme"
	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/appfiles"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/docker/docker/pkg/stdcopy"
	"gopkg.in/yaml.v3"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

func (d *Daemon) handleDaemonStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.DaemonStatusDto{
		Running:    true,
		PID:        int64(os.Getpid()),
		Uptime:     time.Since(d.startTime).Milliseconds(),
		Version:    d.version,
		Workspace:  d.workspaceID,
		SocketPath: d.socketPath,
		Desktop:    config.IsDesktopMode(),
		Locale:     d.daemonCfg.Locale,
	})
}

func (d *Daemon) handleDaemonShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Shutting down"})
	go func() {
		time.Sleep(100 * time.Millisecond)
		d.shutdown()
	}()
}

func (d *Daemon) handleGetNamespace(w http.ResponseWriter, r *http.Request) {
	d.configMu.RLock()
	runtime := d.runtime
	bundleErr := d.bundleError
	appDefs := d.appDefs
	d.configMu.RUnlock()
	if runtime == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	dto := runtime.ToNamespaceDto()
	if bundleErr != "" {
		dto.BundleError = bundleErr
	}
	// When namespace is stopped, runtime clears the app list. Populate from
	// the resolved config so the UI always shows the full service catalog.
	if len(dto.Apps) == 0 && len(appDefs) > 0 {
		dto.Apps = appDefsToStoppedApps(appDefs)
	}
	writeJSON(w, dto)
}

// appDefsToStoppedApps converts resolved app definitions into AppDto entries
// with STOPPED status. Used to populate the UI when namespace is not running.
func appDefsToStoppedApps(defs []appdef.ApplicationDef) []api.AppDto {
	apps := make([]api.AppDto, 0, len(defs))
	for _, def := range defs {
		if def.IsInit {
			continue // skip init containers
		}
		apps = append(apps, api.AppDto{
			Name:   def.Name,
			Status: "STOPPED",
			Image:  def.Image,
			Kind:   namespace.KindToString(def.Kind),
			Ports:  def.Ports,
		})
	}
	return apps
}

func (d *Daemon) handleStartNamespace(w http.ResponseWriter, r *http.Request) {
	d.configMu.RLock()
	runtime, appDefs := d.runtime, d.appDefs
	d.configMu.RUnlock()
	if runtime == nil || appDefs == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	runtime.Start(appDefs)
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Namespace start requested"})
}

func (d *Daemon) handleStopNamespace(w http.ResponseWriter, r *http.Request) {
	d.configMu.RLock()
	runtime := d.runtime
	d.configMu.RUnlock()
	if runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	runtime.Stop()
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Namespace stop requested"})
}

//nolint:nestif // reload orchestrates config read, git pull, bundle resolution, ACME cert obtainment, and runtime regeneration
func (d *Daemon) handleReloadNamespace(w http.ResponseWriter, r *http.Request) {
	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()

	d.configMu.RLock()
	if d.runtime == nil || d.nsConfig == nil || d.bundleDef == nil {
		d.configMu.RUnlock()
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	nsID := d.nsConfig.ID
	d.configMu.RUnlock()

	// Phase 1: slow I/O outside lock (config read, git pull, bundle resolution)
	nsCfg, err := namespace.LoadNamespaceConfig(config.ResolveNamespaceConfigPath(d.workspaceID, nsID))
	if err != nil {
		writeInternalError(w, fmt.Errorf("reload config: %w", err))
		return
	}

	bundlesDataDir := config.DataDir()
	if config.IsDesktopMode() {
		bundlesDataDir = filepath.Join(config.HomeDir(), "ws", d.workspaceID)
	}
	resolver := bundle.NewResolverWithAuth(bundlesDataDir, makeTokenLookup(d.secretReaderFunc()))
	resolveResult, err := resolver.Resolve(nsCfg.BundleRef)
	if err != nil {
		writeInternalError(w, fmt.Errorf("resolve bundle: %w", err))
		return
	}

	appfiles.ExtractTo(d.volumesBase)

	// Self-signed cert: generate if TLS enabled + no cert paths + no LE
	ensureSelfSignedCert(nsCfg)

	// Let's Encrypt: obtain certificate if needed; prepare renewal service for Phase 2
	var newRenewal *acme.RenewalService
	if nsCfg.Proxy.TLS.Enabled && nsCfg.Proxy.TLS.LetsEncrypt && nsCfg.Proxy.Host != "" && nsCfg.Proxy.Host != "localhost" {
		acmeClient := acme.NewClient(config.DataDir(), config.ConfDir(), nsCfg.Proxy.Host)
		if !acmeClient.CertMatchesHost() {
			slog.Info("Obtaining Let's Encrypt certificate on reload", "host", nsCfg.Proxy.Host)
			acmeCtx, acmeCancel := context.WithTimeout(context.Background(), 120*time.Second)
			err := acmeClient.ObtainCertificate(acmeCtx)
			acmeCancel()
			if err != nil {
				slog.Error("Let's Encrypt failed on reload", "err", err)
			}
		}
		if acmeClient.CertMatchesHost() {
			nsCfg.Proxy.TLS.CertPath = acmeClient.CertPath()
			nsCfg.Proxy.TLS.KeyPath = acmeClient.KeyPath()
		}
		newRenewal = acme.NewRenewalService(acmeClient, func() {
			if d.runtime != nil {
				if err := d.runtime.RestartApp("proxy"); err != nil {
					slog.Error("ACME: restart proxy after renewal failed", "err", err)
				}
			}
		})
	}

	var genOpts namespace.GenerateOpts
	if d.runtime != nil {
		genOpts.DetachedApps = d.runtime.ManualStoppedApps()
	}
	genResp := namespace.Generate(nsCfg, resolveResult.Bundle, resolveResult.Workspace, genOpts)

	// Write generated files atomically (prevent partial writes on crash)
	for filePath, content := range genResp.Files {
		destPath := filepath.Join(d.volumesBase, filePath)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil { //nolint:gosec // generated file dirs need 0o755 for container access
			slog.Error("Failed to create dir for generated file", "path", destPath, "err", err)
			continue
		}
		if err := fsutil.AtomicWriteFile(destPath, content, 0o644); err != nil {
			slog.Error("Failed to write generated file", "path", destPath, "err", err)
		}
	}

	// Phase 2: update shared state briefly under write lock
	d.configMu.Lock()
	d.nsConfig = nsCfg
	d.bundleDef = resolveResult.Bundle
	d.workspaceConfig = resolveResult.Workspace
	d.appDefs = genResp.Applications
	// Update ACME renewal service under lock to prevent data race with shutdown
	if d.acmeRenewal != nil {
		d.acmeRenewal.Stop()
	}
	d.acmeRenewal = newRenewal
	d.configMu.Unlock()
	if newRenewal != nil {
		newRenewal.Start()
	}

	if d.cloudCfgServer != nil {
		d.cloudCfgServer.UpdateConfig(genResp.CloudConfig)
	}
	d.runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(resolveResult.Workspace, d.secretReaderFunc()))

	// Phase 3: regenerate runtime (async stop + start) — use local var, not d.appDefs (avoids race)
	d.runtime.Regenerate(genResp.Applications)
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Reload requested"})
}

func (d *Daemon) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	tailStr := r.URL.Query().Get("tail")
	tail := 100
	if tailStr != "" {
		if n, err := strconv.Atoi(tailStr); err == nil {
			tail = n
		}
	}
	if tail > 10000 {
		tail = 10000
	}
	follow := r.URL.Query().Get("follow") == "true"

	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	if follow {
		d.handleAppLogsFollow(w, r, app.ContainerID, tail)
		return
	}

	logCtx, logCancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer logCancel()
	rawLogs, err := d.dockerClient.ContainerLogs(logCtx, app.ContainerID, tail)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	logs := stripAnsi(rawLogs)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(logs))
}

// handleAppLogsFollow streams container logs using Docker follow with proper stdcopy demux.
func (d *Daemon) handleAppLogsFollow(w http.ResponseWriter, r *http.Request, containerID string, tail int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Disable write deadline for long-lived log stream
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{})

	ctx := r.Context()
	reader, err := d.dockerClient.ContainerLogsFollow(ctx, containerID, tail)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")

	// Use stdcopy to demux Docker multiplex headers, writing clean text to the response.
	// stdcopy.StdCopy blocks until the reader is closed (context cancellation or container stop).
	stdcopy.StdCopy(flushWriter{w, flusher}, flushWriter{w, flusher}, reader)
}

// flushWriter wraps an http.ResponseWriter to flush after every write.
type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	fw.f.Flush()
	return n, err
}

func (d *Daemon) handleAppRestart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if d.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	if err := d.runtime.RestartApp(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Restart requested for %s", name)})
}

func (d *Daemon) handleAppStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if d.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	if d.findApp(name) == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	if err := d.runtime.StopApp(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s stopped", name)})
}

func (d *Daemon) handleAppStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if d.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}
	if app.Status == namespace.AppStatusRunning {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeAppAlreadyRunning, fmt.Sprintf("app %q is already running", name))
		return
	}

	if err := d.runtime.StartApp(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s start requested", name)})
}

func (d *Daemon) handleAppInspect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	if app.ContainerID == "" {
		writeJSON(w, api.AppInspectDto{
			Name:   app.Name,
			Status: string(app.Status),
			Image:  app.Def.Image,
		})
		return
	}

	inspCtx, inspCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer inspCancel()
	inspect, err := d.dockerClient.InspectContainer(inspCtx, app.ContainerID)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	var ports []string
	for containerPort, bindings := range inspect.NetworkSettings.Ports {
		for _, b := range bindings {
			ports = append(ports, fmt.Sprintf("%s:%s/%s", b.HostPort, containerPort.Port(), containerPort.Proto()))
		}
	}

	var volumes []string
	for _, m := range inspect.Mounts {
		volumes = append(volumes, fmt.Sprintf("%s:%s", m.Source, m.Destination))
	}

	envVars := make([]string, len(inspect.Config.Env))
	for i, e := range inspect.Config.Env {
		envVars[i] = api.MaskSecretEnv(e)
	}

	dto := api.AppInspectDto{
		Name:         app.Name,
		ContainerID:  app.ContainerID,
		Image:        inspect.Config.Image,
		Status:       string(app.Status),
		State:        inspect.State.Status,
		Ports:        ports,
		Volumes:      volumes,
		Env:          envVars,
		Labels:       inspect.Config.Labels,
		Network:      d.dockerClient.NetworkName(),
		RestartCount: inspect.RestartCount,
		StartedAt:    inspect.State.StartedAt,
	}

	if inspect.State.StartedAt != "" {
		if startedAt, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil {
			dto.Uptime = time.Since(startedAt).Milliseconds()
		}
	}

	writeJSON(w, dto)
}

func (d *Daemon) handleAppExec(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	// Limit request body to 64KB (command array doesn't need more)
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req api.ExecRequestDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	execCtx, execCancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer execCancel()
	output, exitCode, err := d.dockerClient.ExecInContainer(execCtx, app.ContainerID, req.Command)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Cap output at 1MB to prevent OOM
	const maxExecOutput = 1 << 20
	if len(output) > maxExecOutput {
		output = output[:maxExecOutput] + "\n... (output truncated at 1MB)"
	}

	writeJSON(w, api.ExecResultDto{
		ExitCode: int64(exitCode),
		Output:   output,
	})
}

func (d *Daemon) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfgPath := d.activeConfigPath()
	data, err := os.ReadFile(cfgPath) //nolint:gosec // path is constructed from daemon-internal config, not user input
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("config file not found: %s", cfgPath))
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.Write(data)
}

func (d *Daemon) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	cfgPath := d.activeConfigPath()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024)) // 1MB max
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Validate by fully parsing through ParseNamespaceConfig (applies business rules)
	if _, err := namespace.ParseNamespaceConfig(body); err != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidConfig, fmt.Sprintf("invalid config: %s", err.Error()))
		return
	}

	if err := fsutil.AtomicWriteFile(cfgPath, body, 0o600); err != nil {
		writeInternalError(w, fmt.Errorf("save config: %w", err))
		return
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "Configuration saved"})
}

func (d *Daemon) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Disable write deadline for long-lived SSE stream
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, ok2 := d.addSubscriber()
	if !ok2 {
		writeError(w, http.StatusServiceUnavailable, "too many SSE subscribers")
		return
	}
	defer d.removeSubscriber(ch)

	ctx := r.Context()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-ch:
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			ticker.Reset(15 * time.Second)
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (d *Daemon) handleDaemonLogs(w http.ResponseWriter, r *http.Request) {
	logPath := config.DaemonLogPath()

	tailStr := r.URL.Query().Get("tail")
	tail := 200
	if tailStr != "" {
		if n, err := strconv.Atoi(tailStr); err == nil {
			tail = n
		}
	}
	if tail > 10000 {
		tail = 10000
	}

	follow := r.URL.Query().Get("follow") == "true"

	// Read at most last 2MB of the file to avoid OOM on large logs
	const maxReadSize = 2 * 1024 * 1024
	f, err := os.Open(logPath) //nolint:gosec // path is from config.DaemonLogPath(), not user input
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("daemon log not found: %s", logPath))
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	readSize := stat.Size()
	if readSize > maxReadSize {
		if _, seekErr := f.Seek(-maxReadSize, io.SeekEnd); seekErr != nil {
			writeInternalError(w, seekErr)
			return
		}
		readSize = maxReadSize
	}
	data, err := io.ReadAll(io.LimitReader(f, readSize))
	if err != nil {
		writeInternalError(w, err)
		return
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}

	// Disable write deadline before any write in follow mode — the initial tail
	// can be up to 2MB and may exceed the server's 30s WriteTimeout on slow connections.
	if follow {
		if rc := http.NewResponseController(w); rc != nil {
			rc.SetWriteDeadline(time.Time{})
		}
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strings.Join(lines, "\n")))

	if !follow {
		return
	}

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Track file position for incremental reads
	offset := stat.Size()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			f2, err := os.Open(logPath)
			if err != nil {
				return
			}
			st, err := f2.Stat()
			if err != nil {
				f2.Close()
				return
			}
			newSize := st.Size()
			if newSize <= offset {
				// File was rotated or truncated — reset
				if newSize < offset {
					offset = 0
				}
				f2.Close()
				continue
			}
			if _, seekErr := f2.Seek(offset, io.SeekStart); seekErr != nil {
				f2.Close()
				return
			}
			chunk, readErr := io.ReadAll(io.LimitReader(f2, newSize-offset))
			f2.Close()
			if readErr != nil || len(chunk) == 0 {
				continue
			}
			offset = newSize
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

func (d *Daemon) buildDumpData(ctx context.Context) map[string]any {
	dump := make(map[string]any)
	dump["daemon"] = map[string]any{
		"pid":     os.Getpid(),
		"uptime":  time.Since(d.startTime).Milliseconds(),
		"version": d.version,
		"socket":  d.socketPath,
	}
	d.configMu.RLock()
	nsCfg := d.nsConfig
	d.configMu.RUnlock()
	if nsCfg != nil {
		dump["namespace"] = map[string]any{
			"id":     nsCfg.ID,
			"name":   nsCfg.Name,
			"bundle": nsCfg.BundleRef.String(),
		}
	}
	if err := d.dockerClient.Ping(ctx); err != nil {
		dump["docker"] = map[string]any{"available": false, "error": err.Error()}
	} else {
		dump["docker"] = map[string]any{"available": true}
	}
	if d.runtime != nil {
		apps := d.runtime.Apps()
		appList := make([]map[string]string, 0, len(apps))
		for _, app := range apps {
			appList = append(appList, map[string]string{
				"name":   app.Name,
				"status": string(app.Status),
				"image":  app.Def.Image,
			})
		}
		dump["apps"] = appList
	}
	return dump
}

func (d *Daemon) handleSystemDump(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	dump := d.buildDumpData(ctx)

	if r.URL.Query().Get("format") == "zip" {
		// Marshal configs under lock to avoid data races (slices/maps are reference types)
		d.configMu.RLock()
		var nsCfgYAML []byte
		if d.nsConfig != nil {
			masked := maskNamespaceConfigSecrets(d.nsConfig)
			nsCfgYAML, _ = namespace.MarshalNamespaceConfig(masked)
		}
		daemonCfgYAML, _ := yaml.Marshal(d.daemonCfg)
		d.configMu.RUnlock()
		d.writeSystemDumpZip(w, ctx, dump, nsCfgYAML, daemonCfgYAML)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=system-dump.json")
	writeJSON(w, dump)
}

func (d *Daemon) writeSystemDumpZip(w http.ResponseWriter, ctx context.Context, dump map[string]any, nsCfgYAML, daemonCfgYAML []byte) {
	// Extend write deadline for potentially large ZIP with per-app logs
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(5 * time.Minute))

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=system-dump.zip")

	zw := zip.NewWriter(w)
	defer zw.Close()

	// system-info.json
	if infoData, err := json.MarshalIndent(dump, "", "  "); err == nil {
		if fw, err := zw.Create("system-info.json"); err == nil {
			fw.Write(infoData)
		}
	}

	// namespace.yml (pre-marshaled under configMu lock)
	if len(nsCfgYAML) > 0 {
		if fw, err := zw.Create("namespace.yml"); err == nil {
			fw.Write(nsCfgYAML)
		}
	}

	// daemon.yml (pre-marshaled under configMu lock)
	if len(daemonCfgYAML) > 0 {
		if fw, err := zw.Create("daemon.yml"); err == nil {
			fw.Write(daemonCfgYAML)
		}
	}

	// Daemon logs (daemon.log + rotated variants)
	const maxDaemonLogSize = 2 * 1024 * 1024 // 2MB cap per file
	for _, suffix := range []string{"", ".1", ".2", ".3"} {
		logFile := config.DaemonLogPath() + suffix
		data, err := os.ReadFile(logFile)
		if err != nil {
			continue
		}
		if len(data) > maxDaemonLogSize {
			data = data[len(data)-maxDaemonLogSize:]
		}
		fname := "daemon-logs/" + filepath.Base(logFile)
		if fw, err := zw.Create(fname); err == nil {
			fw.Write(data)
		}
	}

	// Per-app logs
	if d.runtime != nil {
		for _, app := range d.runtime.Apps() {
			if app.ContainerID == "" {
				continue
			}
			logs, err := d.dockerClient.ContainerLogs(ctx, app.ContainerID, 500)
			if err != nil {
				continue
			}
			fname := fmt.Sprintf("logs/%s.log", app.Name)
			if fw, err := zw.Create(fname); err == nil {
				fw.Write([]byte(logs))
			}
		}
	}
}

func (d *Daemon) volumesDir() string {
	return filepath.Join(d.volumesBase, "volumes")
}

func (d *Daemon) handleListVolumes(w http.ResponseWriter, _ *http.Request) {
	volDir := d.volumesDir()
	entries, err := os.ReadDir(volDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, []any{})
			return
		}
		writeInternalError(w, err)
		return
	}
	type volumeDto struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	var result []volumeDto
	for _, e := range entries {
		if e.IsDir() {
			result = append(result, volumeDto{
				Name: e.Name(),
				Path: filepath.Join(volDir, e.Name()),
			})
		}
	}
	if result == nil {
		result = []volumeDto{}
	}
	writeJSON(w, result)
}

var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// validateAppName checks if the name matches the valid pattern. Returns false and writes 400 if invalid.
func validateAppName(w http.ResponseWriter, name string) bool {
	if !validNameRegex.MatchString(name) {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest, fmt.Sprintf("invalid app name %q", name))
		return false
	}
	return true
}

func (d *Daemon) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	volPath := filepath.Join(d.volumesDir(), name)
	if _, err := os.Stat(volPath); err != nil {
		writeError(w, http.StatusNotFound, "volume not found")
		return
	}
	// Refuse deletion if namespace is running — volumes may be mounted in containers
	if d.runtime != nil {
		status := d.runtime.Status()
		if status != namespace.NsStatusStopped {
			writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "cannot delete volume while namespace is running — stop the namespace first")
			return
		}
	}
	if err := os.RemoveAll(volPath); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Volume %s deleted", name)})
}

func (d *Daemon) handleGetAppConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}
	// Serialize ApplicationDef to YAML
	data, err := yaml.Marshal(app.Def)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.Write(data)
}

func (d *Daemon) handlePutAppConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if d.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	var newDef appdef.ApplicationDef
	if err := yaml.Unmarshal(body, &newDef); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid YAML: %s", err.Error()))
		return
	}

	// Defense-in-depth: only allow safe mutable fields (environments, resources,
	// startupConditions, livenessProbe, stopTimeout).
	// Structural fields (image, cmd, ports, volumes) are locked to the original
	// definition to prevent container escape.
	oldDef := app.Def
	newDef.Name = name
	newDef.Image = oldDef.Image
	newDef.ImageDigest = oldDef.ImageDigest
	newDef.Cmd = oldDef.Cmd
	newDef.Ports = oldDef.Ports
	newDef.Volumes = oldDef.Volumes
	newDef.VolumesContentHash = oldDef.VolumesContentHash
	newDef.InitContainers = oldDef.InitContainers
	newDef.InitActions = oldDef.InitActions
	newDef.NetworkAliases = oldDef.NetworkAliases
	newDef.Kind = oldDef.Kind
	newDef.IsInit = oldDef.IsInit
	newDef.DependsOn = oldDef.DependsOn
	newDef.ShmSize = oldDef.ShmSize

	if err := d.runtime.UpdateAppDef(name, newDef, true); err != nil {
		writeInternalError(w, err)
		return
	}
	if err := d.runtime.RestartApp(name); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s config updated and restart requested", name)})
}

func (d *Daemon) handleListAppFiles(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	// Collect bind-mounted files from relative bind mounts (./app/... etc.)
	var files []string
	for _, v := range app.Def.Volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) != 2 {
			continue
		}
		hostPath := parts[0]
		if !strings.HasPrefix(hostPath, "./") {
			continue
		}
		// Resolve and check if the path is a regular file (not a directory)
		absPath := filepath.Join(d.volumesBase, hostPath[2:])
		if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
			files = append(files, hostPath)
		}
	}
	writeJSON(w, files)
}

func (d *Daemon) handleGetAppFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	filePath := r.PathValue("path")
	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	// Validate path is a known bind mount
	relPath := "./" + filePath
	if !isAppBindMount(app, relPath) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("file %q is not a bind mount of app %q", filePath, name))
		return
	}

	absPath := filepath.Join(d.volumesBase, filePath)
	if !isPathUnder(absPath, d.volumesBase) {
		writeError(w, http.StatusForbidden, "path outside workspace")
		return
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}

func (d *Daemon) handlePutAppFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	filePath := r.PathValue("path")
	app := d.findApp(name)
	if app == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	relPath := "./" + filePath
	if !isAppBindMount(app, relPath) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("file %q is not a bind mount of app %q", filePath, name))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	absPath := filepath.Join(d.volumesBase, filePath)
	if !isPathUnder(absPath, d.volumesBase) {
		writeError(w, http.StatusForbidden, "path outside workspace")
		return
	}
	if err := fsutil.AtomicWriteFile(absPath, body, 0o644); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: "File updated"})
}

func isPathUnder(path, base string) bool {
	cleanPath := filepath.Clean(path)
	cleanBase := filepath.Clean(base)
	return strings.HasPrefix(cleanPath, cleanBase+string(filepath.Separator))
}

func isAppBindMount(app *namespace.AppRuntime, relPath string) bool {
	for _, v := range app.Def.Volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) >= 2 && parts[0] == relPath {
			return true
		}
	}
	return false
}

func (d *Daemon) handleAppLockToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validateAppName(w, name) {
		return
	}
	if d.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	if d.findApp(name) == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeAppNotFound, fmt.Sprintf("app %q not found", name))
		return
	}

	var body struct {
		Locked bool `json:"locked"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	d.runtime.SetAppLocked(name, body.Locked)
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("App %s lock=%v", name, body.Locked)})
}

//nolint:nestif // health aggregation checks multiple subsystems with per-app status roll-up
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var checks []api.HealthCheckDto

	// Bundle check
	d.configMu.RLock()
	bundleErr := d.bundleError
	d.configMu.RUnlock()
	if bundleErr != "" {
		checks = append(checks, api.HealthCheckDto{Name: "bundle", Status: "error", Message: "Bundle resolution failed: " + bundleErr})
	}

	// Docker check
	if err := d.dockerClient.Ping(ctx); err != nil {
		checks = append(checks, api.HealthCheckDto{Name: "docker", Status: "error", Message: err.Error()})
	} else {
		checks = append(checks, api.HealthCheckDto{Name: "docker", Status: "ok", Message: "Docker daemon is reachable"})
	}

	// Determine overall status: healthy / degraded / unhealthy
	overallStatus := "healthy"

	if d.runtime != nil {
		apps := d.runtime.Apps()
		running, failed := 0, 0
		for _, app := range apps {
			switch app.Status {
			case namespace.AppStatusRunning:
				running++
			case namespace.AppStatusStartFailed, namespace.AppStatusPullFailed:
				failed++
			}
		}

		nsStatus := d.runtime.Status()

		// Determine container-level check status
		containerStatus := "ok"
		if len(apps) == 0 {
			// Namespace stopped or no apps — not a health failure
			containerStatus = "ok"
		} else if running == 0 {
			containerStatus = "error"
		} else if running < len(apps) {
			containerStatus = "warning"
		}
		checks = append(checks, api.HealthCheckDto{
			Name:    "containers",
			Status:  containerStatus,
			Message: fmt.Sprintf("%d/%d apps running, %d failed", running, len(apps), failed),
		})

		for _, app := range apps {
			appStatus := "ok"
			if app.Status != namespace.AppStatusRunning {
				appStatus = "warning"
			}
			checks = append(checks, api.HealthCheckDto{
				Name:    "app:" + app.Name,
				Status:  appStatus,
				Message: string(app.Status),
			})
		}

		// Overall status
		if nsStatus == namespace.NsStatusStalled || (len(apps) > 0 && running == 0) {
			overallStatus = "unhealthy"
		} else if failed > 0 || (len(apps) > 0 && running < len(apps)) {
			overallStatus = "degraded"
		}
	}

	// Check for critical check errors — escalate to unhealthy
	for _, c := range checks {
		if c.Status == "error" {
			overallStatus = "unhealthy"
			break
		}
	}

	writeJSON(w, api.HealthDto{Status: overallStatus, Healthy: overallStatus == "healthy", Checks: checks})
}

func (d *Daemon) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder

	uptimeSeconds := time.Since(d.startTime).Seconds()
	fmt.Fprintf(&b, "# HELP citeck_uptime_seconds Daemon uptime in seconds.\n")
	fmt.Fprintf(&b, "# TYPE citeck_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "citeck_uptime_seconds %.1f\n", uptimeSeconds)

	d.eventMu.Lock()
	sseCount := len(d.eventSubs)
	d.eventMu.Unlock()
	fmt.Fprintf(&b, "# HELP citeck_sse_subscribers Current SSE subscriber count.\n")
	fmt.Fprintf(&b, "# TYPE citeck_sse_subscribers gauge\n")
	fmt.Fprintf(&b, "citeck_sse_subscribers %d\n", sseCount)

	if d.runtime != nil {
		apps := d.runtime.Apps()
		running, failed, total := 0, 0, len(apps)
		for _, app := range apps {
			switch app.Status {
			case namespace.AppStatusRunning:
				running++
			case namespace.AppStatusStartFailed, namespace.AppStatusPullFailed:
				failed++
			}
		}

		nsStatus := string(d.runtime.Status())
		fmt.Fprintf(&b, "# HELP citeck_namespace_status Current namespace status (1=active).\n")
		fmt.Fprintf(&b, "# TYPE citeck_namespace_status gauge\n")
		for _, s := range []string{"STOPPED", "STARTING", "RUNNING", "STOPPING", "STALLED"} {
			val := 0
			if s == nsStatus {
				val = 1
			}
			fmt.Fprintf(&b, "citeck_namespace_status{status=\"%s\"} %d\n", s, val)
		}

		fmt.Fprintf(&b, "# HELP citeck_apps_total Total number of apps.\n")
		fmt.Fprintf(&b, "# TYPE citeck_apps_total gauge\n")
		fmt.Fprintf(&b, "citeck_apps_total %d\n", total)
		fmt.Fprintf(&b, "# HELP citeck_apps_running Number of running apps.\n")
		fmt.Fprintf(&b, "# TYPE citeck_apps_running gauge\n")
		fmt.Fprintf(&b, "citeck_apps_running %d\n", running)
		fmt.Fprintf(&b, "# HELP citeck_apps_failed Number of failed apps.\n")
		fmt.Fprintf(&b, "# TYPE citeck_apps_failed gauge\n")
		fmt.Fprintf(&b, "citeck_apps_failed %d\n", failed)

		// Per-app status gauge
		fmt.Fprintf(&b, "# HELP citeck_app_status Per-app status (1=running, 0=not running).\n")
		fmt.Fprintf(&b, "# TYPE citeck_app_status gauge\n")
		for _, app := range apps {
			val := 0
			if app.Status == namespace.AppStatusRunning {
				val = 1
			}
			fmt.Fprintf(&b, "citeck_app_status{app=\"%s\",status=\"%s\"} %d\n", promEscape(app.Name), promEscape(string(app.Status)), val)
		}
	}

	// Build info
	fmt.Fprintf(&b, "# HELP citeck_build_info Build version and metadata.\n")
	fmt.Fprintf(&b, "# TYPE citeck_build_info gauge\n")
	fmt.Fprintf(&b, "citeck_build_info{version=\"%s\"} 1\n", promEscape(d.version))

	// HTTP request metrics
	httpMetrics.writePrometheus(&b)

	// SSE dropped events
	fmt.Fprintf(&b, "# HELP citeck_sse_events_dropped_total Total SSE events dropped due to slow consumers.\n")
	fmt.Fprintf(&b, "# TYPE citeck_sse_events_dropped_total counter\n")
	fmt.Fprintf(&b, "citeck_sse_events_dropped_total %d\n", d.sseDropped.Load())

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(b.String()))
}

// promEscape escapes a label value for Prometheus text exposition format.
// Only \, \n, and " need escaping per the spec.
func promEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func (d *Daemon) handleSetLogLevel(w http.ResponseWriter, r *http.Request) {
	if d.logLevel == nil {
		writeError(w, http.StatusServiceUnavailable, "log level control not available")
		return
	}
	var req struct {
		Level string `json:"level"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var level slog.Level
	switch strings.ToLower(req.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown level %q (debug, info, warn, error)", req.Level))
		return
	}
	d.logLevel.Set(level)
	slog.Info("Log level changed", "level", level.String())
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("log level set to %s", level.String())})
}

func (d *Daemon) findApp(name string) *namespace.AppRuntime {
	if d.runtime == nil {
		return nil
	}
	return d.runtime.FindApp(name)
}

// stripAnsi removes ANSI escape codes and normalizes tabs (matching Kotlin LogsUtils.normalizeMessage)
// Matches all CSI escape sequences (SGR colors, cursor movement, erase, etc.)
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripAnsi(s string) string {
	s = ansiRegex.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\t", "    ")
	return s
}

// maskNamespaceConfigSecrets returns a shallow copy of the config with passwords in
// authentication.users replaced by "***". Does not mutate the original config.
func maskNamespaceConfigSecrets(cfg *namespace.NamespaceConfig) *namespace.NamespaceConfig {
	out := *cfg
	if len(out.Authentication.Users) > 0 {
		masked := make([]string, len(out.Authentication.Users))
		for i, u := range out.Authentication.Users {
			if idx := strings.Index(u, ":"); idx >= 0 {
				masked[i] = u[:idx+1] + "***"
			} else {
				masked[i] = u
			}
		}
		out.Authentication.Users = masked
	}
	return &out
}

