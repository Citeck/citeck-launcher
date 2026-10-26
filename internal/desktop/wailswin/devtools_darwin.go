//go:build darwin && private_mac_apis

package wailswin

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// citeckEnableDeveloperExtras returns 0 when the flag was set, 1 when the
// window holds no WKWebView, 2 when WebKit refused the key.
static int citeckEnableDeveloperExtras(void *nsWindow) {
	NSWindow *window = (NSWindow *)nsWindow;
	if (![window respondsToSelector:@selector(webView)]) {
		return 1;
	}
	id webView = [window performSelector:@selector(webView)];
	if (![webView isKindOfClass:[WKWebView class]]) {
		return 1;
	}
	@try {
		[((WKWebView *)webView).configuration.preferences setValue:@YES forKey:@"developerExtrasEnabled"];
	} @catch (NSException *exception) {
		return 2;
	}
	return 0;
}
*/
import "C"

import (
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// enableDevTools turns on WKWebView's developerExtrasEnabled, without which
// Wails' OpenDevTools (the private _inspector show, compiled in only with
// -tags private_mac_apis) silently does nothing: on macOS 13.3+ Wails 3.0.0
// sets only the public `inspectable`, which serves Safari's Develop menu but
// not the in-app inspector. alpha.98 set this key itself. It also brings back
// "Inspect Element" in the page's context menu. Must run on the UI thread.
func enableDevTools(win application.Window) {
	ptr := win.NativeWindow()
	if ptr == nil {
		slog.Warn("DevTools not enabled: the window has no native handle yet", "window", win.Name())
		return
	}
	switch C.citeckEnableDeveloperExtras(ptr) {
	case 0:
	case 1:
		slog.Warn("DevTools not enabled: no WKWebView in the window", "window", win.Name())
	default:
		slog.Warn("DevTools not enabled: WebKit refused developerExtrasEnabled", "window", win.Name())
	}
}
