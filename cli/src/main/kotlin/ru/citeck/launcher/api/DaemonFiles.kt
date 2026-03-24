package ru.citeck.launcher.api

import ru.citeck.launcher.cli.daemon.storage.ConfigPaths
import java.nio.file.Path
import java.nio.file.Paths

object DaemonFiles {

    // Socket stays in /run for OS-managed lifecycle and single-instance enforcement
    private val RUN_DIR: Path = Paths.get(
        System.getProperty("citeck.run") ?: "/run/citeck"
    )

    fun ensureDirs() {
        createDirOrFail(RUN_DIR)
        createDirOrFail(ConfigPaths.LOG_DIR)
    }

    private fun createDirOrFail(dir: Path) {
        val file = dir.toFile()
        if (!file.exists() && !file.mkdirs() && !file.exists()) {
            error("Failed to create directory: $dir")
        }
    }

    fun getRunDir(): Path = RUN_DIR

    fun getSocketFile(): Path = RUN_DIR.resolve(DaemonConstants.SOCKET_FILE)

    fun getLogFile(): Path = ConfigPaths.LOG_DIR.resolve("daemon.log")
}
