package ru.citeck.launcher.core.namespace.runtime

enum class AppRuntimeStatus {

    READY_TO_STOP,
    STOPPING,
    STOPPING_FAILED, //stalled

    STOPPED, // final

    READY_TO_PULL,
    PULLING,
    PULL_FAILED, //stalled

    READY_TO_START,
    STARTING,
    START_FAILED, //stalled

    RUNNING; // final

    fun isStoppingState(): Boolean {
        return this == READY_TO_STOP || this == STOPPING || this == STOPPED
    }
}
