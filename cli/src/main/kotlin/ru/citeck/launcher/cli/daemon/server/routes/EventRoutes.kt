package ru.citeck.launcher.cli.daemon.server.routes

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import io.ktor.server.routing.*
import io.ktor.server.websocket.*
import io.ktor.websocket.*
import ru.citeck.launcher.api.ApiPaths
import ru.citeck.launcher.api.dto.EventDto
import ru.citeck.launcher.cli.daemon.services.NamespaceConfigManager
import ru.citeck.launcher.core.namespace.runtime.AppRuntime
import ru.citeck.launcher.core.utils.Disposable
import java.util.concurrent.CopyOnWriteArrayList

fun Routing.eventRoutes(nsManager: NamespaceConfigManager) {

    val mapper = jacksonObjectMapper()

    webSocket(ApiPaths.EVENTS) {

        val disposables = CopyOnWriteArrayList<Disposable>()

        try {
            val runtime = nsManager.getRuntime()
            if (runtime == null) {
                close(CloseReason(CloseReason.Codes.NORMAL, "Namespace is not configured"))
                return@webSocket
            }

            val namespaceId = runtime.namespaceConfig.getValue().id

            disposables.add(
                runtime.nsStatus.watch { before, after ->
                    val event = EventDto(
                        type = "ns_status_changed",
                        namespaceId = namespaceId,
                        before = before.name,
                        after = after.name
                    )
                    trySendEvent(mapper, event)
                }
            )

            val subscribedApps = CopyOnWriteArrayList<String>()

            fun subscribeToApp(appRuntime: AppRuntime) {
                if (subscribedApps.addIfAbsent(appRuntime.name)) {
                    val appName = appRuntime.name
                    disposables.add(
                        appRuntime.status.watch { before, after ->
                            val event = EventDto(
                                type = "app_status_changed",
                                namespaceId = namespaceId,
                                appName = appName,
                                before = before.name,
                                after = after.name
                            )
                            trySendEvent(mapper, event)
                        }
                    )
                }
            }

            for (appRuntime in runtime.appRuntimes.getValue()) {
                subscribeToApp(appRuntime)
            }

            disposables.add(
                runtime.appRuntimes.watch { _, newRuntimes ->
                    for (appRuntime in newRuntimes) {
                        subscribeToApp(appRuntime)
                    }
                }
            )

            for (frame in incoming) {
                // keep connection alive, ignore client messages
            }
        } finally {
            disposables.forEach { it.dispose() }
        }
    }
}

private fun DefaultWebSocketServerSession.trySendEvent(
    mapper: com.fasterxml.jackson.databind.ObjectMapper,
    event: EventDto
) {
    try {
        val json = mapper.writeValueAsString(event)
        outgoing.trySend(Frame.Text(json))
    } catch (_: Throwable) {
        // connection may be closed
    }
}
