package ru.citeck.launcher.cli.daemon.server.routes

import io.ktor.http.*
import io.ktor.server.response.*
import io.ktor.server.routing.*
import ru.citeck.launcher.api.ApiPaths
import ru.citeck.launcher.api.dto.ActionResultDto
import ru.citeck.launcher.api.dto.ErrorDto
import ru.citeck.launcher.cli.daemon.server.converters.NamespaceConverter
import ru.citeck.launcher.cli.daemon.services.NamespaceConfigManager
import ru.citeck.launcher.cli.daemon.storage.ConfigPaths

fun Routing.namespaceRoutes(nsManager: NamespaceConfigManager) {

    get(ApiPaths.NAMESPACE) {
        val ns = NamespaceConverter.toNamespaceDto(nsManager)
        if (ns == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_configured", "Namespace is not configured"))
        } else {
            call.respond(ns)
        }
    }

    post(ApiPaths.NAMESPACE_START) {
        val runtime = nsManager.getRuntime()
        if (runtime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_configured", "Namespace is not configured"))
            return@post
        }
        try {
            runtime.updateAndStart()
            call.respond(ActionResultDto(success = true, message = "Namespace start requested"))
        } catch (e: Throwable) {
            call.respond(
                HttpStatusCode.InternalServerError,
                ErrorDto("start_failed", "Failed to start namespace", e.message ?: "")
            )
        }
    }

    post(ApiPaths.NAMESPACE_STOP) {
        val runtime = nsManager.getRuntime()
        if (runtime == null) {
            call.respond(HttpStatusCode.NotFound, ErrorDto("not_configured", "Namespace is not configured"))
            return@post
        }
        try {
            runtime.stop()
            call.respond(ActionResultDto(success = true, message = "Namespace stop requested"))
        } catch (e: Throwable) {
            call.respond(
                HttpStatusCode.InternalServerError,
                ErrorDto("stop_failed", "Failed to stop namespace", e.message ?: "")
            )
        }
    }

    post(ApiPaths.NAMESPACE_RELOAD) {
        try {
            val success = nsManager.reload()
            if (success) {
                call.respond(ActionResultDto(success = true, message = "Configuration reloaded"))
            } else {
                call.respond(
                    HttpStatusCode.NotFound,
                    ErrorDto("not_found", "Namespace config not found", "Create ${ConfigPaths.NAMESPACE_CONFIG}")
                )
            }
        } catch (e: Throwable) {
            call.respond(
                HttpStatusCode.InternalServerError,
                ErrorDto("reload_failed", "Failed to reload configuration", e.message ?: "")
            )
        }
    }
}
