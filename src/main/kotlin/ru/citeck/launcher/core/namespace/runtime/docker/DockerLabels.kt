package ru.citeck.launcher.core.namespace.runtime.docker

object DockerLabels {
    const val APP_NAME = "citeck.launcher.app.name"
    const val APP_HASH = "citeck.launcher.app.hash"
    const val WORKSPACE = "citeck.launcher.workspace"
    const val NAMESPACE = "citeck.launcher.namespace"
    const val ORIGINAL_NAME = "citeck.launcher.original-name"
    const val LAUNCHER = "citeck.launcher"

    val LAUNCHER_LABEL_PAIR = LAUNCHER to "true"
}
