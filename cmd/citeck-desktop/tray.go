//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync/atomic"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/daemon"
	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// buildTrayMenuFromBackend fetches GET api.DesktopTrayMenu over the daemon
// socket and builds a native Wails menu from it. Verb items dispatch through
// dispatchVerb; the backend "system-dump" item reuses the existing in-flight-
// guarded dump flow (label toggle + dialogs), preserving Kotlin LoadingDialog
// parity. Unknown action kinds/endpoints are skipped.
func buildTrayMenuFromBackend(
	app *application.App,
	socketClient *http.Client,
	dispatchVerb func(verb string, params map[string]any) error,
	dumpInFlight *atomic.Bool,
	socketPath string,
) (*application.Menu, error) {
	req, err := http.NewRequest(http.MethodGet, "http://daemon"+api.DesktopTrayMenu, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build tray-menu request: %w", err)
	}
	resp, err := socketClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch tray menu: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tray-menu endpoint returned %d", resp.StatusCode)
	}
	var tm daemon.TrayMenu
	if err := json.NewDecoder(resp.Body).Decode(&tm); err != nil {
		return nil, fmt.Errorf("decode tray menu: %w", err)
	}

	menu := app.NewMenu()
	for _, item := range tm.Items {
		switch item.Action.Kind {
		case "verb":
			verb := item.Action.Verb
			params := item.Action.Params
			mi := menu.Add(item.Label)
			mi.SetEnabled(item.Enabled)
			mi.OnClick(func(_ *application.Context) {
				if derr := dispatchVerb(verb, params); derr != nil {
					slog.Warn("Tray verb failed", "verb", verb, "err", derr)
				}
			})
		case "backend":
			if item.Action.Endpoint == "/desktop/system-dump" {
				wireDumpItem(menu.Add(item.Label), app, dumpInFlight, socketPath)
				continue
			}
			slog.Warn("Skipping tray item with unsupported backend endpoint", "endpoint", item.Action.Endpoint)
		default:
			slog.Warn("Skipping tray item with unknown action kind", "kind", item.Action.Kind)
		}
	}
	return menu, nil
}

// wireDumpItem attaches the native system-dump flow to a tray menu item. It
// reuses the shared dumpInFlight guard (so a tray dump and a web-UI dump can't
// run in parallel), toggles the item's label/enabled while running, opens the
// containing folder, and shows a success/error dialog — identical to the legacy
// hardcoded dumpItem.
func wireDumpItem(dumpItem *application.MenuItem, app *application.App, dumpInFlight *atomic.Bool, socketPath string) {
	const idle = "Dump System Info"
	dumpItem.OnClick(func(_ *application.Context) {
		if !dumpInFlight.CompareAndSwap(false, true) {
			return
		}
		dumpItem.SetEnabled(false)
		dumpItem.SetLabel(idle + " (running...)")
		go func() {
			defer func() {
				application.InvokeAsync(func() {
					dumpItem.SetLabel(idle)
					dumpItem.SetEnabled(true)
				})
				dumpInFlight.Store(false)
			}()
			zipPath, err := dumpSystemInfo(socketPath)
			application.InvokeAsync(func() {
				if err != nil {
					slog.Error("System dump failed", "err", err)
					app.Dialog.Error().
						SetTitle("System Dump Failed").
						SetMessage(err.Error()).
						Show()
					return
				}
				slog.Info("System dump created", "path", zipPath)
				if openErr := desktop.OpenBrowser("file://" + filepath.Dir(zipPath)); openErr != nil {
					slog.Warn("Failed to open dump folder", "err", openErr)
				}
				app.Dialog.Info().
					SetTitle("System Dump Saved").
					SetMessage("System dump saved to:\n" + zipPath).
					Show()
			})
		}()
	})
}
