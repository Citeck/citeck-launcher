package ru.citeck.launcher.core.config

import ru.citeck.launcher.core.utils.CiteckEnvUtils
import java.nio.file.Path
import java.nio.file.Paths

object AppDir {

    private const val LAUNCHER_APP = "launcher"

    val PATH by lazy { evalAppDir() }

    private fun evalAppDir(): Path {
        val dirPath = if (CiteckEnvUtils.isOsUnix()) {
            val home = System.getenv("HOME")
            if (home.isNullOrBlank()) {
                error("HOME variable is not defined")
            }
            Paths.get(home)
                .resolve(".citeck")
                .resolve(LAUNCHER_APP)
        } else if (CiteckEnvUtils.isOsWindows()) {
            val appData = System.getenv("LOCALAPPDATA")
            if (appData.isNullOrBlank()) {
                error("Env variable localappdata is not defined")
            }
            Paths.get(appData)
                .resolve("Citeck")
                .resolve(LAUNCHER_APP)
        } else if (CiteckEnvUtils.isOsMac()) {
            val home = System.getenv("HOME")
            if (home.isNullOrBlank()) {
                error("HOME variable is not defined")
            }
            Paths.get(home)
                .resolve("Library")
                .resolve("Application Support")
                .resolve("Citeck")
                .resolve(LAUNCHER_APP)
        } else {
            error("OS ${CiteckEnvUtils.getOsType()} not supported yet")
        }
        dirPath.toFile().mkdirs()
        return dirPath
    }
}
