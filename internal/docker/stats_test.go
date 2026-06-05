package docker

import (
	"errors"
	"strings"
	"testing"
)

// TestEffectiveMemoryUsage_CgroupV2 verifies the cgroup v2 case where the
// kernel reports inactive file cache under the `inactive_file` key. Without
// the correction, Docker stats would over-report by ~30% on most workloads.
func TestEffectiveMemoryUsage_CgroupV2(t *testing.T) {
	m := memoryStats{
		Usage: 200 * 1024 * 1024,
		Stats: map[string]int64{
			"inactive_file": 60 * 1024 * 1024,
		},
	}
	got := effectiveMemoryUsage(m)
	want := int64(140 * 1024 * 1024)
	if got != want {
		t.Errorf("effectiveMemoryUsage cgroup v2 = %d, want %d", got, want)
	}
}

// TestEffectiveMemoryUsage_CgroupV1 verifies the cgroup v1 fallback path —
// the kernel reports the cache under `total_inactive_file` (or `cache` as a
// last-resort coarser fallback).
func TestEffectiveMemoryUsage_CgroupV1(t *testing.T) {
	m := memoryStats{
		Usage: 200 * 1024 * 1024,
		Stats: map[string]int64{
			"total_inactive_file": 50 * 1024 * 1024,
		},
	}
	got := effectiveMemoryUsage(m)
	want := int64(150 * 1024 * 1024)
	if got != want {
		t.Errorf("effectiveMemoryUsage cgroup v1 = %d, want %d", got, want)
	}
}

// TestEffectiveMemoryUsage_CacheFallback covers cgroup setups where neither
// inactive_file key is present but a generic `cache` counter is. Less
// accurate than inactive_file but matches the Kotlin launcher's behavior.
func TestEffectiveMemoryUsage_CacheFallback(t *testing.T) {
	m := memoryStats{
		Usage: 200 * 1024 * 1024,
		Stats: map[string]int64{
			"cache": 40 * 1024 * 1024,
		},
	}
	got := effectiveMemoryUsage(m)
	want := int64(160 * 1024 * 1024)
	if got != want {
		t.Errorf("effectiveMemoryUsage cache-fallback = %d, want %d", got, want)
	}
}

// TestEffectiveMemoryUsage_NoStats falls through to raw Usage when none of
// the expected keys are present (older Docker versions, container without
// cgroup memory subsystem, etc.).
func TestEffectiveMemoryUsage_NoStats(t *testing.T) {
	m := memoryStats{
		Usage: 100 * 1024 * 1024,
		Stats: nil,
	}
	got := effectiveMemoryUsage(m)
	if got != m.Usage {
		t.Errorf("effectiveMemoryUsage with nil Stats = %d, want %d", got, m.Usage)
	}
}

// TestEffectiveMemoryUsage_PreferenceOrder verifies that inactive_file wins
// over total_inactive_file when both are reported (some Docker / kernel
// combinations expose both maps simultaneously; v2 is more accurate).
func TestEffectiveMemoryUsage_PreferenceOrder(t *testing.T) {
	m := memoryStats{
		Usage: 200 * 1024 * 1024,
		Stats: map[string]int64{
			"inactive_file":       60 * 1024 * 1024,
			"total_inactive_file": 80 * 1024 * 1024,
			"cache":               90 * 1024 * 1024,
		},
	}
	got := effectiveMemoryUsage(m)
	want := int64(140 * 1024 * 1024)
	if got != want {
		t.Errorf("effectiveMemoryUsage preference order = %d, want %d", got, want)
	}
}

// TestEffectiveMemoryUsage_NegativeProtection guards against pathological
// cases where inactive_file > Usage (kernel reporting glitch). The function
// must clamp to zero rather than returning a negative value that would
// propagate into the AppDto memory percent.
func TestEffectiveMemoryUsage_NegativeProtection(t *testing.T) {
	m := memoryStats{
		Usage: 10 * 1024 * 1024,
		Stats: map[string]int64{
			"inactive_file": 50 * 1024 * 1024,
		},
	}
	got := effectiveMemoryUsage(m)
	if got != 0 {
		t.Errorf("effectiveMemoryUsage negative-protection = %d, want 0", got)
	}
}

// TestParseContainerStats_ThrottleAndMemPercent verifies end-to-end parsing
// produces the expected CPU throttle flag and memory percent. Uses a payload
// shaped like a real docker-stats response (cgroup v2 keys).
func TestParseContainerStats_ThrottleAndMemPercent(t *testing.T) {
	payload := `{
		"cpu_stats": {
			"cpu_usage": {"total_usage": 200000000},
			"system_cpu_usage": 1000000000,
			"online_cpus": 2,
			"throttling_data": {"periods": 100, "throttled_periods": 50, "throttled_time": 0}
		},
		"precpu_stats": {
			"cpu_usage": {"total_usage": 100000000},
			"system_cpu_usage": 800000000,
			"online_cpus": 2,
			"throttling_data": {"periods": 90, "throttled_periods": 40, "throttled_time": 0}
		},
		"memory_stats": {
			"usage": 209715200,
			"limit": 524288000,
			"stats": {"inactive_file": 62914560}
		}
	}`
	stat, err := parseContainerStats(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("parseContainerStats: %v", err)
	}
	if !stat.CPUThrottled {
		t.Error("CPUThrottled should be true (throttled_periods delta = 10)")
	}
	// usage 200 MiB - inactive_file 60 MiB = 140 MiB; limit 500 MiB → 28%
	wantPct := 28.0
	if diff := stat.MemoryPercent - wantPct; diff > 0.1 || diff < -0.1 {
		t.Errorf("MemoryPercent = %.2f, want ~%.2f", stat.MemoryPercent, wantPct)
	}
	if stat.MemUsage != 140*1024*1024 {
		t.Errorf("MemUsage = %d, want %d", stat.MemUsage, 140*1024*1024)
	}
}

// TestParseContainerStats_NoThrottleRecovery confirms that a container that
// was throttled in the past but has the same throttled_periods value as the
// previous sample is reported as not currently throttled.
func TestParseContainerStats_NoThrottleRecovery(t *testing.T) {
	payload := `{
		"cpu_stats": {
			"cpu_usage": {"total_usage": 200000000},
			"system_cpu_usage": 1000000000,
			"online_cpus": 1,
			"throttling_data": {"periods": 100, "throttled_periods": 50, "throttled_time": 0}
		},
		"precpu_stats": {
			"cpu_usage": {"total_usage": 100000000},
			"system_cpu_usage": 800000000,
			"online_cpus": 1,
			"throttling_data": {"periods": 90, "throttled_periods": 50, "throttled_time": 0}
		},
		"memory_stats": {"usage": 0, "limit": 0, "stats": null}
	}`
	stat, err := parseContainerStats(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("parseContainerStats: %v", err)
	}
	if stat.CPUThrottled {
		t.Error("CPUThrottled should be false when throttled_periods delta is 0")
	}
}

// TestIsStaleNetworkError covers both phrasings the Docker daemon may
// return depending on version and which API surface raised the error.
func TestIsStaleNetworkError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"active-endpoints", errors.New("network citeck_network_default has active endpoints"), true},
		{"endpoint-in-use", errors.New("endpoint with ID abc is in use by container xyz"), true},
		{"mixed-case-active", errors.New("network FOO has Active Endpoints"), true},
		{"generic-error", errors.New("connection refused"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		got := IsStaleNetworkError(tt.err)
		if got != tt.want {
			t.Errorf("%s: IsStaleNetworkError(%v) = %v, want %v", tt.name, tt.err, got, tt.want)
		}
	}
}

// TestParseContainerStats_StreamUsesSecondFrame is the regression for "CPU
// always 0.0%": the one-shot stats endpoint returns a zeroed precpu_stats, so
// the CPU% delta collapsed to ~0. Streaming sends two frames; the second's
// precpu_stats is the first's cpu_stats. parseContainerStats must read the
// SECOND frame and compute CPU% from that real ~1s delta.
func TestParseContainerStats_StreamUsesSecondFrame(t *testing.T) {
	frame1 := `{
		"cpu_stats": {"cpu_usage": {"total_usage": 100000000}, "system_cpu_usage": 1000000000, "online_cpus": 2},
		"precpu_stats": {"cpu_usage": {"total_usage": 0}, "system_cpu_usage": 0, "online_cpus": 0},
		"memory_stats": {"usage": 0, "limit": 0}
	}`
	frame2 := `{
		"cpu_stats": {"cpu_usage": {"total_usage": 120000000}, "system_cpu_usage": 1100000000, "online_cpus": 2},
		"precpu_stats": {"cpu_usage": {"total_usage": 100000000}, "system_cpu_usage": 1000000000, "online_cpus": 2},
		"memory_stats": {"usage": 0, "limit": 0}
	}`
	stat, err := parseContainerStats(strings.NewReader(frame1 + frame2))
	if err != nil {
		t.Fatalf("parseContainerStats: %v", err)
	}
	// frame2 delta: (120M-100M)/(1100M-1000M) * 2 * 100 = 0.2*2*100 = 40%
	if diff := stat.CPUPercent - 40.0; diff > 0.1 || diff < -0.1 {
		t.Errorf("CPUPercent = %.2f, want ~40.0 (second-frame delta)", stat.CPUPercent)
	}
}

// A single frame (only one available) still parses, falling back to that frame.
func TestParseContainerStats_SingleFrameFallback(t *testing.T) {
	frame := `{
		"cpu_stats": {"cpu_usage": {"total_usage": 200000000}, "system_cpu_usage": 1000000000, "online_cpus": 2},
		"precpu_stats": {"cpu_usage": {"total_usage": 100000000}, "system_cpu_usage": 800000000, "online_cpus": 2},
		"memory_stats": {"usage": 0, "limit": 0}
	}`
	stat, err := parseContainerStats(strings.NewReader(frame))
	if err != nil {
		t.Fatalf("parseContainerStats: %v", err)
	}
	// (200M-100M)/(1000M-800M) * 2 * 100 = 0.5*2*100 = 100%
	if diff := stat.CPUPercent - 100.0; diff > 0.1 || diff < -0.1 {
		t.Errorf("CPUPercent = %.2f, want ~100.0 (single-frame)", stat.CPUPercent)
	}
}
