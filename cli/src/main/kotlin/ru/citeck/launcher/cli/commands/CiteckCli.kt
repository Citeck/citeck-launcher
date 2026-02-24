package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context

class CiteckCli : CliktCommand(name = "citeck") {

    override fun help(context: Context) = "Citeck Launcher CLI"

    override fun run() = Unit
}
