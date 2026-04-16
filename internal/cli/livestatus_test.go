package cli

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
)

func TestIsAppTerminalFailed(t *testing.T) {
	cases := map[string]bool{
		api.AppStatusRunning:        false,
		api.AppStatusStarting:       false,
		api.AppStatusPulling:        false,
		api.AppStatusReadyToPull:    false,
		api.AppStatusReadyToStart:   false,
		api.AppStatusDepsWaiting:    false,
		api.AppStatusStopped:        false,
		api.AppStatusStopping:       false,
		"":                          false,
		api.AppStatusStartFailed:    true,
		api.AppStatusPullFailed:     true,
		api.AppStatusFailed:         true,
		api.AppStatusStoppingFailed: true,
	}
	for status, want := range cases {
		if got := isAppTerminalFailed(status); got != want {
			t.Errorf("isAppTerminalFailed(%q) = %v, want %v", status, got, want)
		}
	}
}

// TestIsNsPrecommandSnapshot asserts the exact NS-status domain that
// streamLiveStatus treats as "snapshot predates daemon processing" — the
// guard that fixes the `citeck start` after `citeck stop` premature-success
// bug. If anyone changes the set, they must update the call site comment
// in streamLiveStatus and rationalize why (e.g., adding STALLED would cause
// streamLiveStatus to loop forever on a genuinely stalled namespace).
func TestIsNsPrecommandSnapshot(t *testing.T) {
	cases := map[string]bool{
		api.NsStatusStopped:  true,
		api.NsStatusStopping: true,
		api.NsStatusStarting: false,
		api.NsStatusRunning:  false,
		api.NsStatusStalled:  false,
		"":                   false,
	}
	for status, want := range cases {
		if got := isNsPrecommandSnapshot(status); got != want {
			t.Errorf("isNsPrecommandSnapshot(%q) = %v, want %v", status, got, want)
		}
	}
}
