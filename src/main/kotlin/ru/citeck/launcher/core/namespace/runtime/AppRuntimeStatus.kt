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

    companion object {
        private val STARTING_STATUSES = setOf(
            READY_TO_PULL,
            PULLING,
            READY_TO_START,
            STARTING,
            RUNNING
        )
    }

    fun isStoppingState(): Boolean {
        return this == READY_TO_STOP || this == STOPPING || this == STOPPED
    }

    fun isStartingState(): Boolean {
        return STARTING_STATUSES.contains(this)
    }

    fun isStalledState(): Boolean {
        return this == PULL_FAILED || this == START_FAILED || this == STOPPING_FAILED
    }
}
