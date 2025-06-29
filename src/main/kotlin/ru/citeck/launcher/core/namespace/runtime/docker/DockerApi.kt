package ru.citeck.launcher.core.namespace.runtime.docker

import com.github.dockerjava.api.DockerClient
import com.github.dockerjava.api.async.ResultCallbackTemplate
import com.github.dockerjava.api.command.*
import com.github.dockerjava.api.exception.DockerException
import com.github.dockerjava.api.exception.NotFoundException
import com.github.dockerjava.api.model.Container
import com.github.dockerjava.api.model.Frame
import com.github.dockerjava.api.model.Network
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.namespace.NamespaceRef
import java.time.Duration
import java.util.concurrent.TimeUnit

class DockerApi(
    private val client: DockerClient
) {
    companion object {
        private val log = KotlinLogging.logger {}

        private const val GRACEFUL_SHUTDOWN_SECONDS = 10
    }

    fun createVolume(nsRef: NamespaceRef, name: String): CreateVolumeResponse {
        return client.createVolumeCmd()
            .withLabels(
                mapOf(
                    DockerLabels.WORKSPACE to nsRef.workspace,
                    DockerLabels.NAMESPACE to nsRef.namespace
                )
            ).withName(name).exec()
    }

    // +++ VOLUMES +++

    fun getVolumeByName(name: String): InspectVolumeResponse? {
        return client.listVolumesCmd()
            .withFilter("name", listOf(name)).exec()
            .volumes.firstOrNull()
    }

    fun getVolumes(nsRef: NamespaceRef?): List<InspectVolumeResponse> {
        nsRef ?: return emptyList()
        return client.listVolumesCmd()
            .withFilter("label", listOf(
                DockerLabels.NAMESPACE + "=" + nsRef.namespace,
                DockerLabels.WORKSPACE + "=" + nsRef.workspace
            )).exec().volumes
    }

    fun deleteVolume(name: String) {
        client.removeVolumeCmd(name).exec()
    }

    // --- VOLUMES ---

    fun execCreateCmd(containerId: String): ExecCreateCmd {
        return client.execCreateCmd(containerId)
    }

    fun execStartCmd(execId: String): ExecStartCmd {
        return client.execStartCmd(execId)
    }

    fun logContainerCmd(containerId: String): LogContainerCmd {
        return client.logContainerCmd(containerId)
    }

    fun inspectExec(execId: String): InspectExecResponse {
        return client.inspectExecCmd(execId).exec()
    }

    fun getContainers(nsRef: NamespaceRef): List<Container> {
        return client.listContainersCmd()
            .withShowAll(true)
            .withLabelFilter(
                mapOf(
                    DockerLabels.WORKSPACE to nsRef.workspace,
                    DockerLabels.NAMESPACE to nsRef.namespace
                )
            ).exec()
    }

    fun getContainers(nsRef: NamespaceRef, appName: String): List<Container> {
        return client.listContainersCmd()
            .withShowAll(true)
            .withLabelFilter(
                mapOf(
                    DockerLabels.WORKSPACE to nsRef.workspace,
                    DockerLabels.NAMESPACE to nsRef.namespace,
                    DockerLabels.APP_NAME to appName
                )
            ).exec()
    }

    fun stopAndRemoveContainer(container: InspectContainerResponse?) {
        container ?: return
        val containerName = container.name.removePrefix("/")
        stopAndRemoveContainer(container.id, containerName, container.state?.status ?: "unknown")
    }

    fun stopAndRemoveContainer(container: Container) {
        val containerState = container.state
        val containerName = container.names[0].removePrefix("/")
        stopAndRemoveContainer(container.id, containerName, containerState)
    }

    private fun stopAndRemoveContainer(
        containerId: String,
        containerName: String,
        containerState: String
    ) {
        log.info {
            "Stop and remove container ${containerId}. " +
                "Name: $containerName State: $containerState"
        }
        try {
            client.stopContainerCmd(containerId).withTimeout(GRACEFUL_SHUTDOWN_SECONDS).exec()
            val waitUntil = System.currentTimeMillis() + (GRACEFUL_SHUTDOWN_SECONDS + 2) * 1000
            var successfullyStopped = false
            while (System.currentTimeMillis() < waitUntil) {
                val containerInfo = inspectContainerOrNull(containerId)
                if (containerInfo == null || containerInfo.state.running != true) {
                    log.info { "Container '$containerName' was stopped gracefully" }
                    successfullyStopped = true
                    break
                }
                Thread.sleep(1000)
            }
            if (!successfullyStopped) {
                log.warn {
                    "Container '$containerName' did not stop within " +
                        "${GRACEFUL_SHUTDOWN_SECONDS}s, " +
                        "proceeding with force removal"
                }
            }
        } catch (e: DockerException) {
            log.error(e) { "Failed to stop container '$containerName' ($containerId)" }
        }
        client.removeContainerCmd(containerId)
            .withForce(true)
            .withRemoveVolumes(true)
            .exec()
    }

    fun removeContainer(containerId: String) {
        client.removeContainerCmd(containerId).exec()
    }

    fun getExposedPorts(containerId: String): Map<Int, Int> {
        val containerInfo = client.inspectContainerCmd(containerId).exec()
        val result = HashMap<Int, Int>()
        containerInfo.networkSettings.ports.bindings.forEach { (exposedPort, binding) ->
            val publishedPort = binding?.firstOrNull()?.hostPortSpec?.toInt() ?: -1
            if (publishedPort != -1) {
                result[exposedPort.port] = publishedPort
            }
        }
        return result
    }

    fun createContainerCmd(image: String): CreateContainerCmd {
        return client.createContainerCmd(image)
    }

    fun inspectContainer(containerId: String): InspectContainerResponse {
        return client.inspectContainerCmd(containerId).exec()
    }

    fun inspectContainerOrNull(containerId: String?): InspectContainerResponse? {
        if (containerId.isNullOrBlank()) {
            return null
        }
        return try {
            return client.inspectContainerCmd(containerId).exec()
        } catch (e: NotFoundException) {
            null
        }
    }

    fun startContainer(containerId: String) {
        client.startContainerCmd(containerId).exec()
    }

    fun waitContainer(containerId: String): WaitContainerResultCallback {
        return client.waitContainerCmd(containerId).exec(WaitContainerResultCallback())
    }

    fun pullImage(image: String): PullImageCmd {
        return client.pullImageCmd(image)
    }

    fun inspectImage(image: String): InspectImageResponse {
        return client.inspectImageCmd(image).exec()
    }

    fun inspectImageOrNull(image: String): InspectImageResponse? {
        return try {
            client.inspectImageCmd(image).exec()
        } catch (e: NotFoundException) {
            null
        }
    }

    fun getNetworkByName(name: String): Network? {
        return client.listNetworksCmd()
            .withNameFilter(name)
            .exec().firstOrNull()
    }

    fun createBridgeNetwork(name: String): CreateNetworkResponse {
        return client.createNetworkCmd()
            .withDriver("bridge")
            .withName(name)
            .exec()
    }

    /**
     * @return function to cancel watching
     */
    fun watchLogs(
        containerId: String,
        tail: Int,
        logsCallback: (String) -> Unit
    ): AutoCloseable {

        val callback = logContainerCmd(containerId)
            .withTail(tail)
            .withStdErr(true)
            .withStdOut(true)
            .withFollowStream(true)
            .exec(LogsWatchCallback { logsCallback.invoke(it) })

        return callback
    }

    /**
     * @return function to cancel watching
     */
    fun consumeLogs(
        containerId: String,
        tail: Int,
        timeout: Duration,
        logsCallback: (String) -> Unit
    ) {

        val callback = LogsWatchCallback { logsCallback.invoke(it) }
        val closeable = logContainerCmd(containerId)
            .withTail(tail)
            .withStdErr(true)
            .withStdOut(true)
            .withFollowStream(false)
            .exec(callback)

        try {
            callback.awaitCompletion(timeout.toSeconds(), TimeUnit.SECONDS)
        } catch (e: Throwable) {
            log.warn(e) { "Logs consuming cancelled by timeout" }
        } finally {
            closeable.close()
        }
    }

    private class LogsWatchCallback(
        private val logMsgReceiver: (String) -> Unit
    ) : ResultCallbackTemplate<LogsWatchCallback, Frame>() {

        override fun onNext(frame: Frame) {
            if (frame.payload == null || frame.payload.isEmpty()) {
                return
            }
            val payload = String(frame.payload).trimEnd('\n', ' ', '\t', '\r')
            logMsgReceiver.invoke(payload)
        }
    }
}
