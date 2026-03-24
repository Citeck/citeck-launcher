package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import com.github.ajalt.clikt.parameters.arguments.argument
import ru.citeck.launcher.cli.client.DaemonClient

class RestartCmd : CliktCommand(name = "restart") {

    override fun help(context: Context) = "Restart an application"

    private val appName by argument(help = "Application name")

    override fun run() {
        val client = DaemonClient.createOrFail()
        client.use {
            echo("Restarting $appName...")
            val result = it.restartApp(appName)
            if (result == null) {
                echo("Failed to restart '$appName'", err = true)
                return
            }
            echo(result.message)
        }
    }
}
