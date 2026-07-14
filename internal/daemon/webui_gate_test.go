package daemon

import "testing"

// TestWebUITCPAllowed pins the runtime gate for binding the TCP Web UI listener.
// The load-bearing rule: server mode (desktopMode=false) never binds via
// daemon.yml — only the explicit CITECK_SERVER_WEBUI dev/E2E hatch. Desktop mode
// serves over the Unix socket and binds TCP only via the CITECK_DESKTOP_TCP hatch.
func TestWebUITCPAllowed(t *testing.T) {
	cases := []struct {
		name            string
		desktopMode     bool
		allowDesktopTCP bool
		allowServerTCP  bool
		want            bool
	}{
		// Server mode: only the server hatch binds; daemon.yml (and the desktop
		// hatch) can never expose it.
		{"server, no hatches", false, false, false, false},
		{"server, desktop hatch irrelevant", false, true, false, false},
		{"server, server hatch binds", false, false, true, true},
		{"server, both hatches", false, true, true, true},
		// Desktop mode: served over the Unix socket; TCP only via desktop hatch.
		{"desktop, no hatches", true, false, false, false},
		{"desktop, desktop hatch binds", true, true, false, true},
		{"desktop, server hatch irrelevant", true, false, true, false},
		{"desktop, both hatches", true, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := webUITCPAllowed(tc.desktopMode, tc.allowDesktopTCP, tc.allowServerTCP); got != tc.want {
				t.Errorf("webUITCPAllowed(desktop=%v, desktopTCP=%v, serverTCP=%v) = %v, want %v",
					tc.desktopMode, tc.allowDesktopTCP, tc.allowServerTCP, got, tc.want)
			}
		})
	}
}
