package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import com.github.ajalt.clikt.parameters.arguments.argument
import com.github.ajalt.clikt.parameters.arguments.multiple
import ru.citeck.launcher.cli.client.DaemonClient

class ExecCmd : CliktCommand(name = "exec") {

    override fun help(context: Context) = "Execute a command in a container"

    private val appName by argument(help = "Application name")
    private val command by argument(help = "Command to execute").multiple(required = true)

    override fun run() {
        val client = DaemonClient.createOrFail()
        client.use {
            val result = it.execApp(appName, command)
            if (result == null) {
                echo("Failed to execute command in '$appName'. Is the app running?", err = true)
                return
            }
            if (result.output.isNotBlank()) {
                print(result.output)
                if (!result.output.endsWith("\n")) {
                    echo("")
                }
            }
            if (result.exitCode != 0L) {
                throw com.github.ajalt.clikt.core.PrintMessage(
                    "Command exited with code ${result.exitCode}",
                    statusCode = 1
                )
            }
        }
    }
}
