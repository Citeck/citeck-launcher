package ru.citeck.launcher.cli.commands

import com.github.ajalt.clikt.core.CliktCommand
import com.github.ajalt.clikt.core.Context

class VersionCmd : CliktCommand(name = "version") {

    override fun help(context: Context) = "Show version information"

    override fun run() {
        val buildInfo = readBuildInfo()
        echo("Citeck CLI ${buildInfo.version.ifBlank { "unknown" }}")
        if (buildInfo.buildTime.isNotBlank()) {
            echo("Build time: ${buildInfo.buildTime}")
        }
        if (buildInfo.javaVersion.isNotBlank()) {
            echo("Java:       ${buildInfo.javaVersion}")
        }
        echo("OS:         ${System.getProperty("os.name")} ${System.getProperty("os.arch")}")
    }

    private fun readBuildInfo(): BuildInfo {
        return try {
            val content = VersionCmd::class.java.getResourceAsStream("/build-info.json")
                ?.bufferedReader()?.readText() ?: return BuildInfo()
            val version = Regex("\"version\"\\s*:\\s*\"([^\"]+)\"").find(content)?.groupValues?.get(1) ?: ""
            val buildTime = Regex("\"buildTime\"\\s*:\\s*\"([^\"]+)\"").find(content)?.groupValues?.get(1) ?: ""
            val javaVersion = Regex("\"javaVersion\"\\s*:\\s*\"([^\"]+)\"").find(content)?.groupValues?.get(1) ?: ""
            BuildInfo(version, buildTime, javaVersion)
        } catch (_: Throwable) {
            BuildInfo()
        }
    }

    private data class BuildInfo(
        val version: String = "",
        val buildTime: String = "",
        val javaVersion: String = ""
    )
}
