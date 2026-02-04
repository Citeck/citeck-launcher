package ru.citeck.launcher.core.namespace.runtime

data class ContainerStats(
    val cpuPercent: Double = 0.0,
    val cpuCores: Long = 0,
    val cpuThrottledPeriods: Long = 0,
    val cpuThrottledTime: Long = 0,
    val memoryUsage: Long = 0,
    val memoryLimit: Long = 0,
    val memoryPercent: Double = 0.0,
    val memoryCache: Long = 0,
    val timestamp: Long = System.currentTimeMillis()
) {

    companion object {
        val EMPTY = ContainerStats()

        private const val MEMORY_WARNING_THRESHOLD = 90.0
        private const val MEMORY_CRITICAL_THRESHOLD = 95.0

        fun formatCpuPercent(value: Double): String = if (value < 0.01) "0%" else String.format("%.1f%%", value)

        fun formatMemoryShort(usage: Long): String = formatBytes(usage)

        fun formatMemoryFull(usage: Long, limit: Long): String = "${formatBytes(usage)} / ${formatBytes(limit)}"

        fun formatBytes(bytes: Long): String = when {
            bytes >= 1024 * 1024 * 1024 -> String.format("%.1fG", bytes / (1024.0 * 1024 * 1024))
            bytes >= 1024 * 1024 -> String.format("%.0fM", bytes / (1024.0 * 1024))
            bytes >= 1024 -> String.format("%.0fK", bytes / 1024.0)
            else -> "${bytes}B"
        }

        fun formatNanosToMs(nanos: Long): String = String.format("%.1fms", nanos / 1_000_000.0)
    }

    val isMemoryCritical: Boolean
        get() = memoryPercent >= MEMORY_CRITICAL_THRESHOLD

    val isMemoryWarning: Boolean
        get() = memoryPercent >= MEMORY_WARNING_THRESHOLD

    val isCpuThrottled: Boolean
        get() = cpuThrottledPeriods > 0
}

data class DockerSystemInfo(
    val cpuCores: Int = 0,
    val memoryTotal: Long = 0
) {
    val maxCpuPercent: Double
        get() = cpuCores * 100.0

    companion object {
        val EMPTY = DockerSystemInfo()
    }
}

data class NamespaceStats(
    val totalCpuPercent: Double = 0.0,
    val totalMemoryUsage: Long = 0,
    val runningContainers: Int = 0,
    val systemInfo: DockerSystemInfo = DockerSystemInfo.EMPTY
) {
    companion object {
        val EMPTY = NamespaceStats()

        fun aggregate(
            stats: List<ContainerStats>,
            systemInfo: DockerSystemInfo = DockerSystemInfo.EMPTY
        ): NamespaceStats {
            if (stats.isEmpty()) return EMPTY

            val activeStats = stats.filter { it != ContainerStats.EMPTY }
            if (activeStats.isEmpty()) return EMPTY

            return NamespaceStats(
                totalCpuPercent = activeStats.sumOf { it.cpuPercent },
                totalMemoryUsage = activeStats.sumOf { it.memoryUsage },
                runningContainers = activeStats.size,
                systemInfo = systemInfo
            )
        }
    }
}
