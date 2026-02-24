package ru.citeck.launcher.api

object ApiPaths {

    const val API_V1 = "/api/v1"

    const val DAEMON_STATUS = "$API_V1/daemon/status"
    const val DAEMON_SHUTDOWN = "$API_V1/daemon/shutdown"

    const val NAMESPACE = "$API_V1/namespace"
    const val NAMESPACE_START = "$API_V1/namespace/start"
    const val NAMESPACE_STOP = "$API_V1/namespace/stop"
    const val NAMESPACE_RELOAD = "$API_V1/namespace/reload"

    const val EVENTS = "$API_V1/events"
}
