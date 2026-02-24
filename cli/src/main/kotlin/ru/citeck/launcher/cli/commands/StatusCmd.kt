package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import com.github.ajalt.clikt.parameters.options.flag
import com.github.ajalt.clikt.parameters.options.option
import ru.citeck.launcher.cli.client.DaemonClient
import ru.citeck.launcher.cli.output.EventPrinter
import ru.citeck.launcher.cli.output.TableFormatter

class StatusCmd : CliktCommand(name = "status") {

    override fun help(context: Context) = "Show platform status"

    private val watch by option("--watch", "-w", help = "Watch for changes").flag()
    private val apps by option("--apps", "-a", help = "Show application details").flag()

    override fun run() {
        val client = DaemonClient.create()
        if (client == null || !client.isRunning()) {
            client?.close()
            echo("Platform is not running")
            return
        }

        client.use {
            val ns = it.getNamespace()
            if (ns == null) {
                echo("Platform is not configured. Run: citeck install")
                return
            }

            echo("Name:      ${ns.name} (${ns.id})")
            echo("Status:    ${ns.status}")
            echo("Bundle:    ${ns.bundleRef}")

            if (apps || ns.apps.isNotEmpty()) {
                echo("")
                val rows = ns.apps.map { app ->
                    listOf(app.name, app.status, app.image, app.cpu, app.memory)
                }
                echo(TableFormatter.format(listOf("APP", "STATUS", "IMAGE", "CPU", "MEMORY"), rows))
            }

            if (watch) {
                echo("")
                echo("Watching events...")
                EventPrinter.watchEvents(it, ::echo)
            }
        }
    }
}
