package ru.citeck.launcher.core.config.cloud

interface CloudConfig {

    fun getConfig(appName: String, profiles: Collection<String>): Map<String, Any?>
}
