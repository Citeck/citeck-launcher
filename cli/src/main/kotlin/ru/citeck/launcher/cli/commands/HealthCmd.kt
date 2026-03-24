package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import ru.citeck.launcher.cli.client.DaemonClient

class HealthCmd : CliktCommand(name = "health") {

    override fun help(context: Context) = "Check system health"

    override fun run() {
        val client = DaemonClient.createOrFail()
        client.use {
            val health = it.getHealth()
            if (health == null) {
                echo("Failed to get health status", err = true)
                return
            }
            echo(if (health.healthy) "Status: HEALTHY" else "Status: UNHEALTHY")
            echo("")
            for (check in health.checks) {
                val icon = when (check.status) {
                    "ok" -> "[OK]     "
                    "warning" -> "[WARN]   "
                    "error" -> "[ERROR]  "
                    else -> "[?]      "
                }
                echo("$icon ${check.name}: ${check.message}")
            }
        }
    }
}
