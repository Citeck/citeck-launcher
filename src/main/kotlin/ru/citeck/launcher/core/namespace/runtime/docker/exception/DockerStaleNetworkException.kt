package ru.citeck.launcher.core.namespace.runtime.docker.exception

/**
 * Exception thrown when Docker refuses to remove a network due to "active endpoints",
 * even though no containers are attached.
 *
 * This typically indicates a stale or inconsistent internal Docker state.
 * The client may choose to ignore this error, retry later, or prompt the user
 * to restart the Docker daemon or the host system to resolve it.
 */
class DockerStaleNetworkException(cause: Throwable) : RuntimeException(cause)
