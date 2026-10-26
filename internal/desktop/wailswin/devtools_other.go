//go:build (linux && gtk3) || windows || (darwin && !private_mac_apis)

package wailswin

import "github.com/wailsapp/wails/v3/pkg/application"

// enableDevTools has nothing to do here: WebKitGTK and WebView2 enable their
// inspector from DevToolsEnabled, and a macOS build without -tags
// private_mac_apis cannot open the in-app inspector at all (Wails'
// OpenDevTools is a no-op there), see devtools_darwin.go.
func enableDevTools(application.Window) {}
