package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import com.github.ajalt.clikt.parameters.options.flag
import com.github.ajalt.clikt.parameters.options.option
import ru.citeck.launcher.cli.client.DaemonClient
import ru.citeck.launcher.cli.daemon.DaemonLifecycle

class StartCmd : CliktCommand(name = "start") {

    override fun help(context: Context) = "Start the platform"

    private val foreground by option("--foreground", "-f", help = "Run daemon in foreground")
        .flag(default = false)

    override fun run() {
        val client = DaemonClient.create()

        if (client != null && client.isRunning()) {
            client.use { startNamespace(it) }
            return
        }
        client?.close()

        if (foreground) {
            echo("Starting daemon in foreground...")
            DaemonLifecycle.start()
        } else {
            echo("Starting daemon...")
            DaemonLifecycle.startBackground()

            val newClient = waitForDaemon()
            if (newClient == null) {
                echo("Failed to start daemon", err = true)
                return
            }
            newClient.use { startNamespace(it) }
        }
    }

    private fun waitForDaemon(): DaemonClient? {
        val delays = longArrayOf(200, 300, 500, 1000, 1000, 2000, 2000, 3000)
        for (delay in delays) {
            Thread.sleep(delay)
            val client = DaemonClient.create()
            if (client != null && client.isRunning()) {
                return client
            }
            client?.close()
        }
        return null
    }

    private fun startNamespace(client: DaemonClient) {
        val result = client.startNamespace()
        if (result == null) {
            echo("Failed to start platform", err = true)
            return
        }
        echo(result.message)
    }
}
