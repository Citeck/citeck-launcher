package ru.citeck.launcher.cli.daemon.server.routes

import io.ktor.http.*
import io.ktor.server.request.*
import io.ktor.server.response.*
import io.ktor.server.routing.*
import ru.citeck.launcher.api.ApiPaths
import ru.citeck.launcher.api.dto.ActionResultDto
import ru.citeck.launcher.api.dto.AppInspectDto
import ru.citeck.launcher.api.dto.ErrorDto
import ru.citeck.launcher.api.dto.ExecRequestDto
import ru.citeck.launcher.api.dto.ExecResultDto
import ru.citeck.launcher.cli.daemon.services.NamespaceConfigManager
import ru.citeck.launcher.core.namespace.runtime.AppRuntimeStatus
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import java.time.Duration
import java.util.concurrent.TimeUnit

fun Routing.appRoutes(nsManager: NamespaceConfigManager, dockerApi: DockerApi) {

    get("${ApiPaths.APPS}/{name}/logs") {
        val appName = call.parameters["name"] ?: ""
        val tail = call.request.queryParameters["tail"]?.toIntOrNull() ?: 100
        val runtime = nsManager.getRuntime()
        if (runtime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_configured", "Namespace is not configured"))
            return@get
        }
        val appRuntime = runtime.appRuntimes.getValue().find { it.name == appName }
        if (appRuntime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_found", "App '$appName' not found"))
            return@get
        }
        val container = appRuntime.containers.firstOrNull()
        if (container == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_running", "App '$appName' has no running container"))
            return@get
        }
        val lines = StringBuilder()
        dockerApi.consumeLogs(container.id, tail, Duration.ofSeconds(10)) { line ->
            lines.appendLine(line)
        }
        call.respondText(lines.toString(), ContentType.Text.Plain)
    }

    post("${ApiPaths.APPS}/{name}/restart") {
        val appName = call.parameters["name"] ?: ""
        val runtime = nsManager.getRuntime()
        if (runtime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_configured", "Namespace is not configured"))
            return@post
        }
        val appRuntime = runtime.appRuntimes.getValue().find { it.name == appName }
        if (appRuntime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_found", "App '$appName' not found"))
            return@post
        }
        try {
            appRuntime.stop()
            // Wait for stop to complete (up to 30s)
            for (i in 1..60) {
                if (appRuntime.status.getValue() == AppRuntimeStatus.STOPPED) break
                Thread.sleep(500)
            }
            appRuntime.start()
            call.respond(ActionResultDto(success = true, message = "App '$appName' restart requested"))
        } catch (e: Throwable) {
            call.respond(
                HttpStatusCode.InternalServerError,
                ErrorDto("restart_failed", "Failed to restart app '$appName'", e.message ?: "")
            )
        }
    }

    get("${ApiPaths.APPS}/{name}/inspect") {
        val appName = call.parameters["name"] ?: ""
        val runtime = nsManager.getRuntime()
        if (runtime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_configured", "Namespace is not configured"))
            return@get
        }
        val appRuntime = runtime.appRuntimes.getValue().find { it.name == appName }
        if (appRuntime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_found", "App '$appName' not found"))
            return@get
        }
        val container = appRuntime.containers.firstOrNull()
        if (container == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_running", "App '$appName' has no running container"))
            return@get
        }
        try {
            val info = dockerApi.inspectContainer(container.id)
            val state = info.state
            val startedAt = state?.startedAt ?: ""
            val uptime = if (startedAt.isNotBlank()) {
                try {
                    val start = java.time.Instant.parse(startedAt)
                    java.time.Duration.between(start, java.time.Instant.now()).toMillis()
                } catch (_: Throwable) {
                    0L
                }
            } else {
                0L
            }

            val ports = info.networkSettings?.ports?.bindings?.flatMap { (exposed, bindings) ->
                bindings?.map { b -> "${b.hostPortSpec ?: "?"}:${exposed.port}/${exposed.protocol?.name ?: "tcp"}" }
                    ?: listOf("${exposed.port}/${exposed.protocol?.name ?: "tcp"}")
            } ?: emptyList()

            val volumes = info.mounts?.map { m ->
                "${m.source ?: ""}:${m.destination ?: ""}${if (m.rw != true) ":ro" else ""}"
            } ?: emptyList()

            // Filter out sensitive env vars
            val sensitiveKeys = setOf("PASSWORD", "SECRET", "TOKEN", "KEY", "CREDENTIAL")
            val env = info.config?.env?.map { envVar ->
                val key = envVar.substringBefore('=').uppercase()
                if (sensitiveKeys.any { key.contains(it) }) {
                    "${envVar.substringBefore('=')}=***"
                } else {
                    envVar
                }
            } ?: emptyList()

            val networkName = info.networkSettings?.networks?.keys?.firstOrNull() ?: ""

            call.respond(
                AppInspectDto(
                    name = appName,
                    containerId = container.id,
                    image = info.config?.image ?: appRuntime.image,
                    status = appRuntime.status.getValue().name,
                    state = state?.status ?: "",
                    ports = ports,
                    volumes = volumes,
                    env = env,
                    labels = info.config?.labels ?: emptyMap(),
                    network = networkName,
                    restartCount = info.restartCount ?: 0,
                    startedAt = startedAt,
                    uptime = uptime
                )
            )
        } catch (e: Throwable) {
            call.respond(
                HttpStatusCode.InternalServerError,
                ErrorDto("inspect_failed", "Failed to inspect app '$appName'", e.message ?: "")
            )
        }
    }

    post("${ApiPaths.APPS}/{name}/exec") {
        val appName = call.parameters["name"] ?: ""
        val runtime = nsManager.getRuntime()
        if (runtime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_configured", "Namespace is not configured"))
            return@post
        }
        val appRuntime = runtime.appRuntimes.getValue().find { it.name == appName }
        if (appRuntime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_found", "App '$appName' not found"))
            return@post
        }
        val container = appRuntime.containers.firstOrNull()
        if (container == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_running", "App '$appName' has no running container"))
            return@post
        }
        try {
            val request = call.receive<ExecRequestDto>()
            if (request.command.isEmpty()) {
                call.respond(HttpStatusCode.BadRequest, ErrorDto("bad_request", "Command is required"))
                return@post
            }

            val exec = dockerApi.execCreateCmd(container.id)
                .withCmd(*request.command.toTypedArray())
                .withAttachStderr(true)
                .withAttachStdout(true)
                .exec()

            val output = StringBuilder()
            val callback = object :
                com.github.dockerjava.api.async.ResultCallbackTemplate<
                    com.github.dockerjava.api.async.ResultCallback<com.github.dockerjava.api.model.Frame>,
                    com.github.dockerjava.api.model.Frame
                    >() {
                override fun onNext(frame: com.github.dockerjava.api.model.Frame) {
                    if (frame.payload != null && frame.payload.isNotEmpty()) {
                        output.append(String(frame.payload))
                    }
                }
            }

            dockerApi.execStartCmd(exec.id).exec(callback).awaitCompletion(30, TimeUnit.SECONDS)
            val execInfo = dockerApi.inspectExec(exec.id)

            call.respond(
                ExecResultDto(
                    exitCode = execInfo.exitCodeLong ?: -1,
                    output = output.toString()
                )
            )
        } catch (e: Throwable) {
            call.respond(
                HttpStatusCode.InternalServerError,
                ErrorDto("exec_failed", "Failed to exec in app '$appName'", e.message ?: "")
            )
        }
    }
}
