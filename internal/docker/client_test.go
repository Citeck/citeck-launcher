package docker

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// TestBuildContainerConfig_SetsStableHostname pins the Kotlin 1.x parity
// behavior (AppStartAction.withHostName(appDef.name)) that the Go rewrite
// dropped: the container hostname must equal the app name, NOT default to the
// Docker container ID. For RabbitMQ this keeps the node name (rabbit@<hostname>)
// — and therefore its Mnesia data dir — stable across container recreates;
// without it every recreate boots a fresh DB and loses the citeck SA + perms.
func TestBuildContainerConfig_SetsStableHostname(t *testing.T) {
	app := appdef.ApplicationDef{Name: "rabbitmq", Image: "rabbitmq:4.1.2-management"}
	cfg := buildContainerConfig(app, nil, nil, nil)
	if cfg.Hostname != "rabbitmq" {
		t.Errorf("Hostname = %q, want %q (stable node name across recreate)", cfg.Hostname, "rabbitmq")
	}
}

// TestBuildHostConfig_PinsSwapToMemory pins the Kotlin 1.x parity behavior
// (AppStartAction.withMemorySwap(memory)): when a memory limit is set, swap is
// pinned equal to it so the limit is a hard RAM cap with NO swap. Docker
// otherwise defaults MemorySwap to 2×Memory, letting a capped container spill
// into swap — thrashing instead of a clean cap (bad for brokers and DBs).
func TestBuildHostConfig_PinsSwapToMemory(t *testing.T) {
	const limit = 512 * 1024 * 1024
	app := appdef.ApplicationDef{Name: "rabbitmq"}
	hc := buildHostConfig(app, nil, nil, "citeck_net", limit, 0)
	if hc.Memory != limit {
		t.Errorf("Memory = %d, want %d", hc.Memory, limit)
	}
	if hc.MemorySwap != limit {
		t.Errorf("MemorySwap = %d, want %d (swap disabled: MemorySwap==Memory)", hc.MemorySwap, limit)
	}
}

// TestBuildHostConfig_NoMemoryLimitLeavesSwapUnset ensures we don't pin swap
// when there's no memory limit (leaving Docker defaults untouched).
func TestBuildHostConfig_NoMemoryLimitLeavesSwapUnset(t *testing.T) {
	app := appdef.ApplicationDef{Name: "x"}
	hc := buildHostConfig(app, nil, nil, "citeck_net", 0, 0)
	if hc.Memory != 0 || hc.MemorySwap != 0 {
		t.Errorf("Memory=%d MemorySwap=%d, want both 0 when no limit", hc.Memory, hc.MemorySwap)
	}
}

func TestParseMemory(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"128m", 128 * 1024 * 1024},
		{"1g", 1024 * 1024 * 1024},
		{"512k", 512 * 1024},
		{"256M", 256 * 1024 * 1024},
		{"3G", 3 * 1024 * 1024 * 1024},
		{"", 0},
		{"64m", 64 * 1024 * 1024},
		// Fractional values (e.g. 1.5 GiB) must not truncate the decimal.
		{"1.5g", 1536 * 1024 * 1024},
		{"0.5g", 512 * 1024 * 1024},
		{"2.5m", 2621440},
		{"1.5G", 1536 * 1024 * 1024},
		{"garbage", 0},
	}
	for _, tt := range tests {
		got := ParseMemory(tt.input)
		if got != tt.want {
			t.Errorf("ParseMemory(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
