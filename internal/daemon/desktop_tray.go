package daemon

import (
	"net/http"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
)

// TrayAction describes what a tray item does: either a native wrapper verb or a
// backend endpoint the wrapper POSTs.
type TrayAction struct {
	Kind     string         `json:"kind"`               // "verb" | "backend"
	Verb     string         `json:"verb,omitempty"`     // when Kind=="verb"
	Params   map[string]any `json:"params,omitempty"`   // verb params
	Endpoint string         `json:"endpoint,omitempty"` // when Kind=="backend"
}

// TrayItem is one tray menu entry.
type TrayItem struct {
	ID      string     `json:"id"`
	Label   string     `json:"label"`
	Enabled bool       `json:"enabled"`
	Action  TrayAction `json:"action"`
}

// TrayMenu is the full backend-defined tray menu.
type TrayMenu struct {
	Items []TrayItem `json:"items"`
}

// trayLabel returns the localized label for key, falling back to the English
// default. i18n.T is global-locale and returns the key itself when absent, so
// the fallback applies until desktop.tray.* keys are added (current parity).
func trayLabel(key, fallback string) string {
	if s := i18n.T(key); s != "" && s != key {
		return s
	}
	return fallback
}

// buildTrayMenu returns the localized tray menu. Mirrors the legacy hardcoded
// items 1:1 (Open / Dump System Info / Open Launcher Dir / DevTools / Exit).
func buildTrayMenu() TrayMenu {
	return TrayMenu{Items: []TrayItem{
		{ID: "open", Label: trayLabel("desktop.tray.open", "Open"), Enabled: true,
			Action: TrayAction{Kind: "verb", Verb: "window.focus"}},
		{ID: "dump", Label: trayLabel("desktop.tray.dump", "Dump System Info"), Enabled: true,
			Action: TrayAction{Kind: "backend", Endpoint: "/desktop/system-dump"}},
		{ID: "open-dir", Label: trayLabel("desktop.tray.openDir", "Open Launcher Dir"), Enabled: true,
			Action: TrayAction{Kind: "verb", Verb: "shell.openPath",
				Params: map[string]any{"path": config.HomeDir()}}},
		{ID: "devtools", Label: trayLabel("desktop.tray.devtools", "DevTools"), Enabled: true,
			Action: TrayAction{Kind: "verb", Verb: "devtools.open"}},
		{ID: "exit", Label: trayLabel("desktop.tray.exit", "Exit"), Enabled: true,
			Action: TrayAction{Kind: "verb", Verb: "app.quit"}},
	}}
}

func (d *Daemon) handleTrayMenu(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, buildTrayMenu())
}
