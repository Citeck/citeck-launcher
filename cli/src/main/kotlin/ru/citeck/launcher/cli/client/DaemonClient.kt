package ru.citeck.launcher.cli.client

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import com.github.ajalt.clikt.core.PrintMessage
import io.ktor.client.*
import io.ktor.client.engine.cio.*
import io.ktor.client.plugins.websocket.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import io.ktor.http.*
import io.ktor.websocket.*
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.api.ApiPaths
import ru.citeck.launcher.api.DaemonFiles
import ru.citeck.launcher.api.dto.*
import kotlin.io.path.exists

class DaemonClient(private val socketPath: String) : AutoCloseable {

    companion object {
        private val mapper = jacksonObjectMapper()

        fun create(): DaemonClient? {
            val socketFile = DaemonFiles.getSocketFile()
            if (!socketFile.exists()) return null
            return DaemonClient(socketFile.toString())
        }

        fun createOrFail(): DaemonClient {
            return create()
                ?: throw PrintMessage(
                    "Daemon is not running. Start it with: citeck start",
                    statusCode = 1
                )
        }
    }

    private val httpClient = HttpClient(CIO) {
        install(WebSockets)
    }

    fun isRunning(): Boolean {
        return try {
            getStatus() != null
        } catch (_: Throwable) {
            false
        }
    }

    fun getStatus(): DaemonStatusDto? {
        return get(ApiPaths.DAEMON_STATUS)
    }

    fun shutdown(): ActionResultDto {
        return post(ApiPaths.DAEMON_SHUTDOWN)
            ?: ActionResultDto(success = false, message = "Failed to connect to daemon")
    }

    fun getNamespace(): NamespaceDto? {
        return get(ApiPaths.NAMESPACE)
    }

    fun startNamespace(): ActionResultDto? {
        return post(ApiPaths.NAMESPACE_START)
    }

    fun stopNamespace(): ActionResultDto? {
        return post(ApiPaths.NAMESPACE_STOP)
    }

    fun reloadNamespace(): ActionResultDto? {
        return post(ApiPaths.NAMESPACE_RELOAD)
    }

    fun getAppLogs(name: String, tail: Int = 100): String? {
        return getText(ApiPaths.appLogs(name) + "?tail=$tail")
    }

    fun restartApp(name: String): ActionResultDto? {
        return post(ApiPaths.appRestart(name))
    }

    fun inspectApp(name: String): AppInspectDto? {
        return get(ApiPaths.appInspect(name))
    }

    fun execApp(name: String, command: List<String>): ExecResultDto? {
        return postBody(ApiPaths.appExec(name), ExecRequestDto(command))
    }

    fun getHealth(): HealthDto? {
        return get(ApiPaths.HEALTH)
    }

    fun watchEvents(
        onMessage: (String) -> Unit,
        onClose: () -> Unit
    ) {
        runBlocking {
            try {
                httpClient.webSocket(
                    request = {
                        url {
                            protocol = URLProtocol.WS
                            host = "localhost"
                            encodedPath = ApiPaths.EVENTS
                        }
                        unixSocket(socketPath)
                    }
                ) {
                    for (frame in incoming) {
                        if (frame is Frame.Text) {
                            onMessage(frame.readText())
                        }
                    }
                }
            } catch (_: Throwable) {
                // Connection closed or error
            } finally {
                onClose()
            }
        }
    }

    private inline fun <reified T> get(path: String): T? {
        return runBlocking {
            try {
                val response: HttpResponse = httpClient.get {
                    url {
                        protocol = URLProtocol.HTTP
                        host = "localhost"
                        encodedPath = path
                    }
                    unixSocket(socketPath)
                    accept(ContentType.Application.Json)
                }
                if (response.status.isSuccess()) {
                    mapper.readValue<T>(response.bodyAsText())
                } else {
                    null
                }
            } catch (_: Throwable) {
                null
            }
        }
    }

    private fun getText(path: String): String? {
        return runBlocking {
            try {
                val response: HttpResponse = httpClient.get {
                    url {
                        protocol = URLProtocol.HTTP
                        host = "localhost"
                        encodedPath = path.substringBefore('?')
                        val query = path.substringAfter('?', "")
                        if (query.isNotEmpty()) {
                            query.split('&').forEach { param ->
                                val (k, v) = param.split('=', limit = 2)
                                parameters.append(k, v)
                            }
                        }
                    }
                    unixSocket(socketPath)
                }
                if (response.status.isSuccess()) {
                    response.bodyAsText()
                } else {
                    null
                }
            } catch (_: Throwable) {
                null
            }
        }
    }

    private inline fun <reified T> postBody(path: String, body: Any): T? {
        return runBlocking {
            try {
                val response: HttpResponse = httpClient.post {
                    url {
                        protocol = URLProtocol.HTTP
                        host = "localhost"
                        encodedPath = path
                    }
                    unixSocket(socketPath)
                    contentType(ContentType.Application.Json)
                    accept(ContentType.Application.Json)
                    setBody(mapper.writeValueAsString(body))
                }
                if (response.status.isSuccess()) {
                    mapper.readValue<T>(response.bodyAsText())
                } else {
                    null
                }
            } catch (_: Throwable) {
                null
            }
        }
    }

    private inline fun <reified T> post(path: String): T? {
        return runBlocking {
            try {
                val response: HttpResponse = httpClient.post {
                    url {
                        protocol = URLProtocol.HTTP
                        host = "localhost"
                        encodedPath = path
                    }
                    unixSocket(socketPath)
                    contentType(ContentType.Application.Json)
                    accept(ContentType.Application.Json)
                }
                if (response.status.isSuccess()) {
                    mapper.readValue<T>(response.bodyAsText())
                } else {
                    null
                }
            } catch (_: Throwable) {
                null
            }
        }
    }

    override fun close() {
        httpClient.close()
    }
}
