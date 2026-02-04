package ru.citeck.launcher.core.config.cloud

interface MutableCloudConfig : CloudConfig {

    fun put(appName: String, config: Any)

    fun put(appName: String, profile: String, config: Any)
}
