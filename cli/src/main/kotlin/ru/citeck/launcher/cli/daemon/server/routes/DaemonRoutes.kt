package ru.citeck.launcher.cli.daemon.server.routes

import io.ktor.server.response.*
import io.ktor.server.routing.*
import ru.citeck.launcher.api.ApiPaths
import ru.citeck.launcher.api.DaemonFiles
import ru.citeck.launcher.api.dto.ActionResultDto
import ru.citeck.launcher.api.dto.DaemonStatusDto
import kotlin.concurrent.thread

private val startTime = System.currentTimeMillis()

fun Routing.daemonRoutes(onShutdown: () -> Unit) {

    get(ApiPaths.DAEMON_STATUS) {
        call.respond(
            DaemonStatusDto(
                running = true,
                pid = ProcessHandle.current().pid(),
                uptime = System.currentTimeMillis() - startTime,
                version = VersionHolder.version,
                workspace = "daemon",
                socketPath = DaemonFiles.getSocketFile().toString()
            )
        )
    }

    post(ApiPaths.DAEMON_SHUTDOWN) {
        call.respond(ActionResultDto(success = true, message = "Daemon is shutting down"))
        thread(start = true, name = "daemon-shutdown") {
            Thread.sleep(500)
            onShutdown()
        }
    }
}

private object VersionHolder {

    val version: String by lazy {
        try {
            VersionHolder::class.java.getResourceAsStream("/build-info.json")?.use { stream ->
                val content = stream.bufferedReader().readText()
                val versionMatch = Regex("\"version\"\\s*:\\s*\"([^\"]+)\"").find(content)
                versionMatch?.groupValues?.get(1) ?: ""
            } ?: ""
        } catch (_: Throwable) {
            ""
        }
    }
}
