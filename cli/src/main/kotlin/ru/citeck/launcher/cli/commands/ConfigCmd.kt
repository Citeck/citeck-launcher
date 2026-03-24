package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import com.github.ajalt.clikt.core.subcommands
import ru.citeck.launcher.cli.client.DaemonClient
import ru.citeck.launcher.cli.daemon.storage.ConfigPaths
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.utils.json.Yaml
import java.nio.file.Paths
import kotlin.io.path.exists
import kotlin.io.path.isReadable
import kotlin.io.path.readText

class ConfigCmd : CliktCommand(name = "config") {

    override fun help(context: Context) = "Configuration management"

    init {
        subcommands(ShowSubCmd(), ValidateSubCmd())
    }

    override fun run() = Unit
}

class ShowSubCmd : CliktCommand(name = "show") {

    override fun help(context: Context) = "Show current configuration"

    override fun run() {
        val configFile = ConfigPaths.NAMESPACE_CONFIG
        if (!configFile.exists()) {
            echo("No configuration found at $configFile")
            echo("Run 'citeck install' to create one.")
            return
        }
        echo("# $configFile")
        echo("")
        echo(configFile.readText())
    }
}

class ValidateSubCmd : CliktCommand(name = "validate") {

    override fun help(context: Context) = "Validate current configuration"

    override fun run() {
        val configFile = ConfigPaths.NAMESPACE_CONFIG
        var errors = 0

        // Check config file exists
        if (!configFile.exists()) {
            echo("[ERROR] Config file not found: $configFile")
            echo("Run 'citeck install' to create one.")
            return
        }
        echo("[OK]    Config file: $configFile")

        // Parse YAML
        val config: NamespaceConfig
        try {
            config = Yaml.read(configFile, NamespaceConfig::class)
            echo("[OK]    YAML parsing: valid")
        } catch (e: Throwable) {
            echo("[ERROR] YAML parsing failed: ${e.message}")
            return
        }

        // Check authentication
        when (config.authentication.type) {
            NamespaceConfig.AuthenticationType.BASIC -> {
                if (config.authentication.users.isEmpty()) {
                    echo("[ERROR] Authentication: BASIC requires at least one user")
                    errors++
                } else {
                    echo("[OK]    Authentication: BASIC (${config.authentication.users.size} users)")
                }
            }
            NamespaceConfig.AuthenticationType.KEYCLOAK -> {
                echo("[OK]    Authentication: KEYCLOAK")
            }
        }

        // Check proxy
        val proxy = config.citeckProxy
        if (proxy.port < 1 || proxy.port > 65535) {
            echo("[ERROR] Proxy port: ${proxy.port} is out of range (1-65535)")
            errors++
        } else {
            echo("[OK]    Proxy port: ${proxy.port}")
        }
        echo("[OK]    Proxy host: ${proxy.host.ifBlank { "localhost (default)" }}")

        // Check TLS
        if (proxy.tls.enabled) {
            val certPath = Paths.get(proxy.tls.certPath)
            val keyPath = Paths.get(proxy.tls.keyPath)
            if (proxy.tls.certPath.isBlank()) {
                echo("[ERROR] TLS cert path is empty")
                errors++
            } else if (!certPath.exists()) {
                echo("[ERROR] TLS cert not found: ${proxy.tls.certPath}")
                errors++
            } else if (!certPath.isReadable()) {
                echo("[ERROR] TLS cert not readable: ${proxy.tls.certPath}")
                errors++
            } else {
                echo("[OK]    TLS cert: ${proxy.tls.certPath}")
            }
            if (proxy.tls.keyPath.isBlank()) {
                echo("[ERROR] TLS key path is empty")
                errors++
            } else if (!keyPath.exists()) {
                echo("[ERROR] TLS key not found: ${proxy.tls.keyPath}")
                errors++
            } else if (!keyPath.isReadable()) {
                echo("[ERROR] TLS key not readable: ${proxy.tls.keyPath}")
                errors++
            } else {
                echo("[OK]    TLS key: ${proxy.tls.keyPath}")
            }
        } else {
            echo("[OK]    TLS: disabled")
        }

        // Check bundle ref
        if (!config.bundleRef.isEmpty()) {
            echo("[OK]    Bundle: ${config.bundleRef}")
        } else {
            echo("[WARN]  Bundle: not specified (will use default)")
        }

        // Check Docker (if daemon is running)
        val client = DaemonClient.create()
        if (client != null && client.isRunning()) {
            client.use {
                val health = it.getHealth()
                if (health != null) {
                    val dockerCheck = health.checks.find { c -> c.name == "docker" }
                    if (dockerCheck?.status == "ok") {
                        echo("[OK]    Docker: reachable")
                    } else {
                        echo("[ERROR] Docker: ${dockerCheck?.message ?: "unreachable"}")
                        errors++
                    }
                }
            }
        } else {
            client?.close()
            echo("[SKIP]  Docker check: daemon not running")
        }

        echo("")
        if (errors > 0) {
            echo("Validation FAILED with $errors error(s)")
        } else {
            echo("Validation PASSED")
        }
    }
}
