package ru.citeck.launcher.cli.daemon.server.routes

import io.ktor.server.response.*
import io.ktor.server.routing.*
import ru.citeck.launcher.api.ApiPaths
import ru.citeck.launcher.api.dto.HealthCheckDto
import ru.citeck.launcher.api.dto.HealthDto
import ru.citeck.launcher.cli.daemon.services.NamespaceConfigManager
import ru.citeck.launcher.core.namespace.runtime.AppRuntimeStatus
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import java.io.File

fun Routing.healthRoutes(nsManager: NamespaceConfigManager, dockerApi: DockerApi) {

    get(ApiPaths.HEALTH) {
        val checks = mutableListOf<HealthCheckDto>()

        // Check Docker
        try {
            dockerApi.ping()
            checks.add(HealthCheckDto("docker", "ok", "Docker daemon is reachable"))
        } catch (e: Throwable) {
            checks.add(HealthCheckDto("docker", "error", "Docker daemon unreachable: ${e.message}"))
        }

        // Check containers
        val runtime = nsManager.getRuntime()
        if (runtime != null) {
            val apps = runtime.appRuntimes.getValue()
            val running = apps.count { it.status.getValue() == AppRuntimeStatus.RUNNING }
            val total = apps.size
            val status = if (running == total) "ok" else "warning"
            checks.add(HealthCheckDto("containers", status, "$running/$total apps running"))

            for (app in apps) {
                val appStatus = app.status.getValue()
                val appCheck = if (appStatus == AppRuntimeStatus.RUNNING) "ok" else "warning"
                checks.add(HealthCheckDto("app:${app.name}", appCheck, appStatus.name))
            }
        } else {
            checks.add(HealthCheckDto("containers", "warning", "Namespace not configured"))
        }

        // Check disk space
        val dataDir = File("/opt/citeck/data")
        if (dataDir.exists()) {
            val usableSpace = dataDir.usableSpace
            val totalSpace = dataDir.totalSpace
            val usedPercent = if (totalSpace > 0) ((totalSpace - usableSpace) * 100 / totalSpace) else 0
            val usableGb = usableSpace / (1024 * 1024 * 1024)
            val status = when {
                usableGb < 1 -> "error"
                usableGb < 5 -> "warning"
                else -> "ok"
            }
            checks.add(HealthCheckDto("disk", status, "${usableGb}GB free ($usedPercent% used)"))
        }

        // Check memory
        val rt = Runtime.getRuntime()
        val freeMemMb = rt.freeMemory() / (1024 * 1024)
        val totalMemMb = rt.totalMemory() / (1024 * 1024)
        val maxMemMb = rt.maxMemory() / (1024 * 1024)
        checks.add(HealthCheckDto("jvm_memory", "ok", "${freeMemMb}MB free / ${totalMemMb}MB total / ${maxMemMb}MB max"))

        val healthy = checks.none { it.status == "error" }
        call.respond(HealthDto(healthy = healthy, checks = checks))
    }
}
