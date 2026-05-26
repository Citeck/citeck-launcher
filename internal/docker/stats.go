package docker

import (
	"encoding/json"
	"io"
)

type dockerStats struct {
	CPUStats    cpuStats    `json:"cpu_stats"`
	PreCPUStats cpuStats    `json:"precpu_stats"`
	MemoryStats memoryStats `json:"memory_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage int64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemCPUUsage int64 `json:"system_cpu_usage"`
	OnlineCPUs     int   `json:"online_cpus"`
	ThrottlingData struct {
		Periods          uint64 `json:"periods"`
		ThrottledPeriods uint64 `json:"throttled_periods"`
		ThrottledTime    uint64 `json:"throttled_time"`
	} `json:"throttling_data"`
}

type memoryStats struct {
	Usage int64            `json:"usage"`
	Limit int64            `json:"limit"`
	Stats map[string]int64 `json:"stats"`
}

// effectiveMemoryUsage returns Usage minus the inactive file cache so the
// reported number matches what `docker stats` shows and what the Kotlin
// launcher displayed. On cgroup v2 the cache is reported as `inactive_file`;
// on cgroup v1 it is `total_inactive_file` with `cache` as a coarse fallback.
// Missing keys collapse to the raw Usage value.
func effectiveMemoryUsage(m memoryStats) int64 {
	usage := m.Usage
	if usage <= 0 || len(m.Stats) == 0 {
		return usage
	}
	// Order matches the Kotlin launcher's lookup order — see
	// docs/porting/10 §6 (item #16) and the original ContainerStats.kt.
	for _, key := range []string{"inactive_file", "total_inactive_file", "cache"} {
		if v, ok := m.Stats[key]; ok && v > 0 {
			adjusted := usage - v
			if adjusted < 0 {
				return 0
			}
			return adjusted
		}
	}
	return usage
}

func parseContainerStats(reader io.Reader) (*ContainerStat, error) {
	var stats dockerStats
	if err := json.NewDecoder(reader).Decode(&stats); err != nil {
		return nil, err //nolint:wrapcheck // JSON decode in thin wrapper
	}

	cpuPercent := 0.0
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemCPUUsage - stats.PreCPUStats.SystemCPUUsage)
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(stats.CPUStats.OnlineCPUs) * 100.0
	}

	memUsage := effectiveMemoryUsage(stats.MemoryStats)
	memLimit := stats.MemoryStats.Limit
	memPercent := 0.0
	if memLimit > 0 {
		memPercent = float64(memUsage) / float64(memLimit) * 100.0
		if memPercent < 0 {
			memPercent = 0
		}
		if memPercent > 100 {
			memPercent = 100
		}
	}

	// Throttle delta — only periods accumulated since the previous sample
	// count as "currently throttled", which avoids permanently flagging a
	// container that hit its CPU quota once at startup and recovered.
	throttleDelta := int64(stats.CPUStats.ThrottlingData.ThrottledPeriods) - int64(stats.PreCPUStats.ThrottlingData.ThrottledPeriods)
	cpuThrottled := throttleDelta > 0

	return &ContainerStat{
		CPUPercent:    cpuPercent,
		CPUThrottled:  cpuThrottled,
		MemUsage:      memUsage,
		MemLimit:      memLimit,
		MemoryPercent: memPercent,
	}, nil
}
