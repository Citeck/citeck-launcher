package ru.citeck.launcher.core.utils

object CiteckEnvUtils {

    enum class OsType {
        WINDOWS,
        MAC,
        UNIX,
        UNKNOWN
    }

    private val isContainerEnv: Boolean = System.getProperties().containsKey("ecos.env.container")

    private val osType: OsType = run {
        val typeId = System.getProperty("os.name").lowercase()
        when {
            typeId.indexOf("win") >= 0 -> OsType.WINDOWS
            typeId.indexOf("mac") >= 0 -> OsType.MAC
            typeId.indexOf("nix") >= 0 ||
                typeId.indexOf("nux") >= 0 ||
                typeId.indexOf("aix") >= 0 -> OsType.UNIX
            typeId.indexOf("aix") >= 0 -> OsType.UNIX
            else -> OsType.UNKNOWN
        }
    }

    @JvmStatic
    fun isContainerEnv(): Boolean {
        return isContainerEnv
    }

    @JvmStatic
    fun isNotContainerEnv(): Boolean {
        return !isContainerEnv()
    }

    // OS Type

    @JvmStatic
    fun isOsWindows(): Boolean {
        return osType == OsType.WINDOWS
    }

    @JvmStatic
    fun isOsUnix(): Boolean {
        return osType == OsType.UNIX
    }

    @JvmStatic
    fun isOsMac(): Boolean {
        return osType == OsType.MAC
    }

    @JvmStatic
    fun getOsType(): OsType = osType
}
