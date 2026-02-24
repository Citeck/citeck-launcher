package ru.citeck.launcher.cli

import com.github.ajalt.clikt.core.main
import com.github.ajalt.clikt.core.subcommands
import ru.citeck.launcher.cli.commands.CiteckCli
import ru.citeck.launcher.cli.commands.InstallCmd
import ru.citeck.launcher.cli.commands.ReloadCmd
import ru.citeck.launcher.cli.commands.StartCmd
import ru.citeck.launcher.cli.commands.StatusCmd
import ru.citeck.launcher.cli.commands.StopCmd
import ru.citeck.launcher.cli.commands.UninstallCmd

fun main(args: Array<String>) {
    CiteckCli()
        .subcommands(
            InstallCmd(),
            UninstallCmd(),
            StartCmd(),
            StopCmd(),
            StatusCmd(),
            ReloadCmd()
        )
        .main(args)
}
