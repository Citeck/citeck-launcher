package ru.citeck.launcher.cli.daemon.server

import com.fasterxml.jackson.databind.SerializationFeature
import io.ktor.serialization.jackson.*
import io.ktor.server.application.*
import io.ktor.server.cio.*
import io.ktor.server.engine.*
import io.ktor.server.plugins.contentnegotiation.*
import io.ktor.server.routing.*
import io.ktor.server.websocket.*
import ru.citeck.launcher.api.DaemonFiles
import ru.citeck.launcher.cli.daemon.server.routes.daemonRoutes
import ru.citeck.launcher.cli.daemon.server.routes.eventRoutes
import ru.citeck.launcher.cli.daemon.server.routes.namespaceRoutes
import ru.citeck.launcher.cli.daemon.services.NamespaceConfigManager
import kotlin.io.path.deleteIfExists
import kotlin.time.Duration.Companion.seconds

class DaemonServer(
    private val nsManager: NamespaceConfigManager,
    private val onShutdown: () -> Unit
) {

    private var server: EmbeddedServer<*, *>? = null

    fun start() {
        val socketPath = DaemonFiles.getSocketFile().toString()

        // Delete stale socket file if exists
        DaemonFiles.getSocketFile().deleteIfExists()

        server = embeddedServer(CIO, configure = {
            unixConnector(socketPath)
        }) {
            install(ContentNegotiation) {
                jackson {
                    disable(SerializationFeature.FAIL_ON_EMPTY_BEANS)
                }
            }
            install(WebSockets) {
                pingPeriod = 15.seconds
                timeout = 30.seconds
                maxFrameSize = Long.MAX_VALUE
                masking = false
            }
            routing {
                daemonRoutes(onShutdown)
                namespaceRoutes(nsManager)
                eventRoutes(nsManager)
            }
        }.start(wait = false)
    }

    fun stop() {
        server?.stop(gracePeriodMillis = 1000, timeoutMillis = 5000)
        server = null
        DaemonFiles.getSocketFile().deleteIfExists()
    }
}
