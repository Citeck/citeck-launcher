package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import com.github.ajalt.clikt.parameters.arguments.argument
import com.github.ajalt.clikt.parameters.options.default
import com.github.ajalt.clikt.parameters.options.flag
import com.github.ajalt.clikt.parameters.options.option
import com.github.ajalt.clikt.parameters.types.int
import ru.citeck.launcher.cli.client.DaemonClient

class LogsCmd : CliktCommand(name = "logs") {

    override fun help(context: Context) = "Show container logs for an app"

    private val appName by argument(help = "Application name")
    private val tail by option("--tail", "-n", help = "Number of lines to show").int().default(100)
    private val follow by option("--follow", "-f", help = "Follow log output").flag()

    override fun run() {
        val client = DaemonClient.createOrFail()
        client.use {
            if (follow) {
                // For follow mode, use the event-based approach
                echo("Following logs for $appName (Ctrl+C to stop)...")
                val shutdownHook = Thread { /* allow graceful exit */ }
                Runtime.getRuntime().addShutdownHook(shutdownHook)
                val logs = it.getAppLogs(appName, tail)
                if (logs == null) {
                    echo("Failed to get logs for '$appName'. Is the app running?", err = true)
                    return
                }
                print(logs)
                // Poll for new logs
                try {
                    while (true) {
                        Thread.sleep(2000)
                        val newLogs = it.getAppLogs(appName, 10)
                        if (newLogs != null && newLogs.isNotBlank()) {
                            print(newLogs)
                        }
                    }
                } catch (_: InterruptedException) {
                    // exit gracefully
                }
            } else {
                val logs = it.getAppLogs(appName, tail)
                if (logs == null) {
                    echo("Failed to get logs for '$appName'. Is the app running?", err = true)
                    return
                }
                print(logs)
            }
        }
    }
}
