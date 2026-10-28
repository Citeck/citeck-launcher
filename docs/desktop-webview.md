# Desktop webview readiness, zoom and DevTools

The desktop wrapper owns Wails and native windows. The daemon supplies the web
UI. In-app auto-update replaces the daemon payload, not the installed wrapper;
therefore wrapper and frontend versions must be considered separately.

## Runtime readiness and event delivery

Wails queues `WebviewWindow.ExecJS` until the window handles
`wails:runtime:ready`. Native daemon events use this same path. Importing the
full Wails JS runtime is one way to send the message, but is not required.

| Source/version | Readiness sender | Consequence |
| --- | --- | --- |
| `ece634d` baseline of the zoom branch | Neither the page nor this wrapper's predecessor sent it | Wrapper JS could remain queued. This is the scope of the original missing-handshake diagnosis. |
| Released `v2.17.3` (`a1156ff`) frontend | `connectDesktopEvents` subscribes, then calls `_wails.invoke('wails:runtime:ready')` when available | The subscribed page releases queued events and wrapper JS; this release must not be described as universally having a dead bridge. |
| Wrapper with `MarkRuntimeReadyOnFirstLoad` | Go calls `HandleMessage('wails:runtime:ready')` on each window's first native page-load event | Readiness does not depend on the daemon page having its own handshake, including secondary windows. |

The zoom branch was based on `ece634d`, before the `v2.17.3` frontend fix.
When integrating with that release's changes, preserve the frontend subscription
ordering and event recovery code. The wrapper helper and frontend handshake can
coexist. The helper's `sync.Once` limits its own call, not every possible
`WindowRuntimeReady` event: Wails also processes messages from the page.

Register the helper for every wrapper-created webview. It listens for:

- macOS: `Mac.WebViewDidFinishNavigation`;
- Linux: `Linux.WindowLoadFinished`;
- Windows: `Windows.WebViewNavigationCompleted`.

Before the first load, queued reload requests wait rather than interrupting the
initial navigation. Wails keeps its readiness flag across later navigations.
Readiness is not proof that the page has installed an event subscriber or that
an operation completed; event reconciliation remains necessary.

`reloadWebview` uses `window.location.reload()` to preserve the current route,
including `/window/...` pages. Wails alpha.98's native `Reload()` was a no-op on
macOS; Wails 3.0.0 implements it, but the common JS reload path is retained.
`UpdateDialog` also polls the daemon's `currentVersion` to reload after a swap.

## macOS page zoom and wrapper compatibility

`UIZoom` is 1.1. Linux uses WebKitGTK zoom level, Windows uses WebView2 zoom
factor, and Wails 3.0.0 uses WKWebView `pageZoom` on macOS 11+. Page zoom adjusts
the layout viewport; alpha.98 used pinch magnification, which can enlarge the
rendering beyond the window without reflowing the layout.

macOS does not apply `options.Zoom` during window initialization. The wrapper
applies it explicitly:

- Main window: `PrepareWindowOnFirstLoad`, on the first native macOS load.
- Secondary windows: `PrepareWindow`, immediately after native window creation.

Zoom setup does not depend on `WindowRuntimeReady`. Applying it only once keeps
a reload from undoing the user's subsequent Cmd+/Cmd- changes. Actual Size
resets to 1.0, not to `UIZoom`.

A `v2.17.3` frontend can also activate an older wrapper's runtime-ready zoom
hook. On alpha.98/macOS that hook still uses pinch magnification. The Go helper
does not retroactively change installed wrappers: obtaining Wails 3.0.0 page
zoom requires upgrading the desktop wrapper, not just its daemon payload.

The Go 1.26 toolchain requires macOS 12 or newer, above Wails' pageZoom API
floor. The older `LSMinimumSystemVersion=10.15` in packaging metadata does not
establish Catalina support; its mismatch predates this branch. See the
[official Go requirements](https://go.dev/wiki/MinimumRequirements#macos-n%C3%A9e-os-x-aka-darwin).

## DevTools and build tags

The pinned Wails 3.0.0 macOS in-app inspector needs both:

- `private_mac_apis`, which includes Wails' private inspector implementation;
- WKWebView `developerExtrasEnabled`, set by `enableDevTools` from `PrepareWindow`.

The public `inspectable` setting alone serves Safari's Develop menu and does
not replace this in-app inspector setup. Keep the Makefile's `DESKTOP_TAGS` and
`packaging/macos/release.sh` aligned. Linux additionally selects the pinned
Wails version's GTK3 backend with `gtk3`; other platforms ignore that tag.

## Verification boundaries

`runtime_ready_test.go` tests first-load/once behavior with a fake window.
`runtime_ready_wiring_test.go` checks wrapper call sites and the pinned Wails
message handler. `devtools_guard_test.go` checks macOS build tags. These guards
do not establish native macOS rendering, inspector behavior or notarization.

Linux Wails/WebKit smoke tests can check the shared readiness and event path;
Windows cross-vet checks compilation. Native macOS verification is needed for
page zoom, Cmd+/Cmd-/Actual Size, reload persistence, secondary-window layout
and F12/tray DevTools. Release signing and notarization checks remain separate.
