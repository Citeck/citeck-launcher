package ru.citeck.launcher.core.namespace.runtime.docker.exception

import org.apache.commons.lang3.exception.ExceptionUtils
import java.net.ConnectException

class DockerNotAvailableException(cause: Throwable) : RuntimeException("Docker is not available", cause) {

    val isDockerNotRunning: Boolean

    init {
        val rootCause = ExceptionUtils.getRootCause(cause) ?: cause
        isDockerNotRunning = rootCause is ConnectException ||
            rootCause::class.simpleName == "ConnectionClosedException"
    }
}
