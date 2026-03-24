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
}

type memoryStats struct {
	Usage int64 `json:"usage"`
	Limit int64 `json:"limit"`
}

func parseContainerStats(reader io.Reader) (*ContainerStat, error) {
	var stats dockerStats
	if err := json.NewDecoder(reader).Decode(&stats); err != nil {
		return nil, err
	}

	cpuPercent := 0.0
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemCPUUsage - stats.PreCPUStats.SystemCPUUsage)
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(stats.CPUStats.OnlineCPUs) * 100.0
	}

	return &ContainerStat{
		CPUPercent: cpuPercent,
		MemUsage:   stats.MemoryStats.Usage,
		MemLimit:   stats.MemoryStats.Limit,
	}, nil
}
