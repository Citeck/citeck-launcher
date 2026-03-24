package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import com.github.ajalt.clikt.parameters.arguments.argument
import ru.citeck.launcher.cli.client.DaemonClient

class InspectCmd : CliktCommand(name = "inspect") {

    override fun help(context: Context) = "Show detailed container information for an app"

    private val appName by argument(help = "Application name")

    override fun run() {
        val client = DaemonClient.createOrFail()
        client.use {
            val info = it.inspectApp(appName)
            if (info == null) {
                echo("Failed to inspect '$appName'. Is the app running?", err = true)
                return
            }
            echo("Name:         ${info.name}")
            echo("Container ID: ${info.containerId.take(12)}")
            echo("Image:        ${info.image}")
            echo("Status:       ${info.status}")
            echo("State:        ${info.state}")
            echo("Network:      ${info.network}")
            echo("Started at:   ${info.startedAt}")
            echo("Uptime:       ${formatUptime(info.uptime)}")
            echo("Restarts:     ${info.restartCount}")
            if (info.ports.isNotEmpty()) {
                echo("")
                echo("Ports:")
                info.ports.forEach { echo("  $it") }
            }
            if (info.volumes.isNotEmpty()) {
                echo("")
                echo("Volumes:")
                info.volumes.forEach { echo("  $it") }
            }
            if (info.env.isNotEmpty()) {
                echo("")
                echo("Environment:")
                info.env.forEach { echo("  $it") }
            }
        }
    }

    private fun formatUptime(ms: Long): String {
        if (ms <= 0) return "N/A"
        val seconds = ms / 1000
        val minutes = seconds / 60
        val hours = minutes / 60
        val days = hours / 24
        return when {
            days > 0 -> "${days}d ${hours % 24}h ${minutes % 60}m"
            hours > 0 -> "${hours}h ${minutes % 60}m ${seconds % 60}s"
            minutes > 0 -> "${minutes}m ${seconds % 60}s"
            else -> "${seconds}s"
        }
    }
}
