package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import ru.citeck.launcher.cli.client.DaemonClient

class ReloadCmd : CliktCommand(name = "reload") {

    override fun help(context: Context) = "Reload configuration"

    override fun run() {
        val client = DaemonClient.create()
        if (client == null || !client.isRunning()) {
            client?.close()
            echo("Platform is not running. Start it with: citeck start")
            return
        }

        client.use {
            val result = it.reloadNamespace()
            if (result == null) {
                echo("Failed to reload configuration", err = true)
                return
            }
            if (result.success) {
                echo(result.message)
            } else {
                echo("Error: ${result.message}", err = true)
            }
        }
    }
}
