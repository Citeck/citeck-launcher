package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import com.github.ajalt.clikt.parameters.options.flag
import com.github.ajalt.clikt.parameters.options.option
import ru.citeck.launcher.cli.client.DaemonClient

class StopCmd : CliktCommand(name = "stop") {

    override fun help(context: Context) = "Stop the platform"

    private val shutdown by option("--shutdown", "-s", help = "Also shutdown the daemon")
        .flag(default = false)

    override fun run() {
        val client = DaemonClient.create()
        if (client == null || !client.isRunning()) {
            client?.close()
            echo("Platform is not running")
            return
        }

        client.use {
            val result = it.stopNamespace()
            if (result == null) {
                echo("Failed to stop platform", err = true)
                return
            }
            echo(result.message)

            if (shutdown) {
                echo("Shutting down daemon...")
                it.shutdown()
            }
        }
    }
}
