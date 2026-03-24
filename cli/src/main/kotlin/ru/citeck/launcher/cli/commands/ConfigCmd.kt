package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context
import com.github.ajalt.clikt.core.subcommands
import ru.citeck.launcher.cli.daemon.storage.ConfigPaths
import kotlin.io.path.exists
import kotlin.io.path.readText

class ConfigCmd : CliktCommand(name = "config") {

    override fun help(context: Context) = "Configuration management"

    init {
        subcommands(ShowSubCmd())
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
