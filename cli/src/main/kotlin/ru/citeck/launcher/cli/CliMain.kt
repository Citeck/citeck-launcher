package ru.citeck.launcher.cli

import com.github.ajalt.clikt.core.main
import com.github.ajalt.clikt.core.subcommands
import ru.citeck.launcher.cli.commands.*

fun main(args: Array<String>) {
    CiteckCli()
        .subcommands(
            InstallCmd(),
            UninstallCmd(),
            StartCmd(),
            StopCmd(),
            StatusCmd(),
            ReloadCmd(),
            LogsCmd(),
            RestartCmd(),
            InspectCmd(),
            ExecCmd(),
            VersionCmd(),
            HealthCmd(),
            ConfigCmd()
        )
        .main(args)
}
