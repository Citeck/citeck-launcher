package ru.citeck.launcher.cli.daemon.server.converters

import ru.citeck.launcher.api.dto.AppDto
import ru.citeck.launcher.api.dto.NamespaceDto
import ru.citeck.launcher.cli.daemon.services.NamespaceConfigManager
import ru.citeck.launcher.core.namespace.runtime.ContainerStats

object NamespaceConverter {

    fun toNamespaceDto(nsManager: NamespaceConfigManager): NamespaceDto? {
        val runtime = nsManager.getRuntime() ?: return null
        val ns = runtime.namespaceConfig.getValue()

        val apps = runtime.appRuntimes.getValue().map { appRuntime ->
            val stats = appRuntime.containerStats.getValue()
            AppDto(
                name = appRuntime.name,
                status = appRuntime.status.getValue().name,
                image = appRuntime.image,
                detached = appRuntime.isDetached,
                cpu = formatCpu(stats),
                memory = formatMemory(stats)
            )
        }

        return NamespaceDto(
            id = ns.id,
            name = ns.name,
            status = runtime.nsStatus.getValue().name,
            bundleRef = ns.bundleRef.toString(),
            apps = apps
        )
    }

    private fun formatCpu(stats: ContainerStats): String {
        if (stats == ContainerStats.EMPTY) return ""
        return ContainerStats.formatCpuPercent(stats.cpuPercent)
    }

    private fun formatMemory(stats: ContainerStats): String {
        if (stats == ContainerStats.EMPTY) return ""
        return ContainerStats.formatMemoryFull(stats.memoryUsage, stats.memoryLimit)
    }
}
