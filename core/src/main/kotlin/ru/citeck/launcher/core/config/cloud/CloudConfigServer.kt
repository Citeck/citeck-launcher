package ru.citeck.launcher.core.config.cloud

import io.ktor.http.*
import io.ktor.server.cio.*
import io.ktor.server.engine.*
import io.ktor.server.response.*
import io.ktor.server.routing.*
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.json.Json

class CloudConfigServer : Disposable {

    companion object {
        private const val PORT = 8761
    }

    private var server: EmbeddedServer<*, *>? = null
    var cloudConfig: CloudConfig? = null

    fun init() {

        this.server = embeddedServer(factory = CIO, port = PORT) {
            routing {
                get("/config/{appName}/{profiles?}/{...}") {

                    val appName = call.parameters["appName"].orEmpty()
                    val profiles = call.parameters["profiles"]
                        .orEmpty()
                        .split(",")
                        .filter { it.isNotBlank() }

                    val propsSources = mutableListOf(
                        PropertiesSource(
                            "citeck-launcher://application.yml",
                            mapOf(
                                "ecos.webapp.web.authenticators.jwt.secret" to
                                    "my-secret-key-which-should-be-changed-in-production-and-be-base64-encoded",
                                "configserver.status" to "Citeck Launcher Config Server"
                            )
                        )
                    )

                    cloudConfig?.getConfig(appName, profiles)?.let {
                        if (it.isNotEmpty()) {
                            propsSources.add(PropertiesSource("citeck-launcher://$appName.yml", it))
                        }
                    }

                    val response = ConfigResponse(
                        appName,
                        profiles,
                        propertySources = propsSources
                    )

                    call.respondBytes(contentType = ContentType.Application.Json, status = HttpStatusCode.OK) {
                        Json.toBytes(response)
                    }
                }
            }
        }.start()
    }

    override fun dispose() {
        server?.stop(gracePeriodMillis = 0, timeoutMillis = 1000)
        server = null
    }

    @Suppress("unused")
    private class ConfigResponse(
        val name: String,
        val profiles: List<String>,
        val label: String = "main",
        val version: String = "1",
        val propertySources: List<PropertiesSource>
    )

    @Suppress("unused")
    private class PropertiesSource(
        val name: String,
        val source: Map<String, Any?>
    )
}
