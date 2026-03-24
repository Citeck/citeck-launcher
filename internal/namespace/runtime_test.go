package namespace

import (
	"testing"
)

func TestNsRuntimeStatus_Values(t *testing.T) {
	tests := []struct {
		status NsRuntimeStatus
		want   string
	}{
		{NsStatusStopped, "STOPPED"},
		{NsStatusStarting, "STARTING"},
		{NsStatusRunning, "RUNNING"},
		{NsStatusStopping, "STOPPING"},
		{NsStatusStalled, "STALLED"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("expected %s, got %s", tt.want, tt.status)
		}
	}
}

func TestAppRuntimeStatus_Values(t *testing.T) {
	tests := []struct {
		status AppRuntimeStatus
		want   string
	}{
		{AppStatusReadyToPull, "READY_TO_PULL"},
		{AppStatusPulling, "PULLING"},
		{AppStatusPullFailed, "PULL_FAILED"},
		{AppStatusReadyToStart, "READY_TO_START"},
		{AppStatusDepsWaiting, "DEPS_WAITING"},
		{AppStatusStarting, "STARTING"},
		{AppStatusRunning, "RUNNING"},
		{AppStatusFailed, "FAILED"},
		{AppStatusStartFailed, "START_FAILED"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("expected %s, got %s", tt.want, tt.status)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0K"},
		{1024 * 1024, "1M"},
		{512 * 1024 * 1024, "512M"},
		{1024 * 1024 * 1024, "1.0G"},
		{3 * 1024 * 1024 * 1024, "3.0G"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %s, want %s", tt.bytes, got, tt.want)
		}
	}
}
