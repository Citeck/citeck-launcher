package ru.citeck.launcher.core.namespace.runtime.docker

import com.github.dockerjava.api.DockerClient
import com.github.dockerjava.api.async.ResultCallbackTemplate
import com.github.dockerjava.api.command.*
import com.github.dockerjava.api.exception.DockerException
import com.github.dockerjava.api.exception.NotFoundException
import com.github.dockerjava.api.exception.NotModifiedException
import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Container
import com.github.dockerjava.api.model.Frame
import com.github.dockerjava.api.model.HostConfig
import com.github.dockerjava.api.model.Network
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.namespace.NamespaceRef
import ru.citeck.launcher.core.snapshot.NamespaceSnapshotMeta
import ru.citeck.launcher.core.snapshot.VolumeSnapshotMeta
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.CompressionAlg
import ru.citeck.launcher.core.utils.file.FileUtils
import ru.citeck.launcher.core.utils.ZipUtils
import ru.citeck.launcher.core.utils.json.Json
import java.nio.file.Path
import java.time.Duration
import java.time.Instant
import java.util.concurrent.TimeUnit
import kotlin.io.path.absolutePathString
import kotlin.io.path.name

// inspect volume data
// docker run -it --rm -v citeck-volume-zookeeper-cv45o7y-DEFAULT:/volume  busybox:1.37 sh
class DockerApi(
    private val client: DockerClient
) {
    companion object {
        private val log = KotlinLogging.logger {}
        private const val UTILS_IMAGE = "registry.citeck.ru/community/launcher-utils:1.0"

        private const val GRACEFUL_SHUTDOWN_SECONDS = 10
    }

    fun createVolume(nsRef: NamespaceRef, originalName: String, name: String): CreateVolumeResponse {
        return client.createVolumeCmd()
            .withLabels(
                mapOf(
                    DockerLabels.WORKSPACE to nsRef.workspace,
                    DockerLabels.NAMESPACE to nsRef.namespace,
                    DockerLabels.ORIGINAL_NAME to originalName,
                    DockerLabels.LAUNCHER_LABEL_PAIR
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
        } catch (_: NotModifiedException) {
            log.debug { "Container already stopped '$containerName' ($containerId)" }
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
        } catch (_: NotFoundException) {
            null
        }
    }

    fun getNetworkByName(name: String): Network? {
        return client.listNetworksCmd()
            .withNameFilter(name)
            .exec().firstOrNull()
    }

    fun deleteNetwork(name: String) {
        val network = getNetworkByName(name) ?: return
        client.removeNetworkCmd(network.id).exec()
    }

    fun exportSnapshot(
        nsRef: NamespaceRef,
        targetFile: Path,
        alg: CompressionAlg,
        actionStatus: ActionStatus.Mut = ActionStatus.Mut()
    ) {

        actionStatus.message = "initialization"

        val tarAlgParam = when (alg) {
            CompressionAlg.ZSTD -> "--zstd"
            CompressionAlg.XZ -> "--xz"
        }

        if (getContainers(nsRef).isNotEmpty()) {
            error("Containers should be stopped before exporting snapshot")
        }
        val exportStartedAt = System.currentTimeMillis()
        log.info { "Begin snapshot exporting for namespace $nsRef" }

        actionStatus.progress = 0.01f

        val volumes = getVolumes(nsRef)
        if (volumes.isEmpty()) {
            error("No volumes found in namespace $nsRef")
        }
        log.info { "Volumes: ${volumes.joinToString { it.name }}" }

        actionStatus.progress = 0.02f

        if (inspectImageOrNull(UTILS_IMAGE) == null) {
            actionStatus.message = "Pull utils image"
            log.info { "Image $UTILS_IMAGE is not exists locally. Let's pull it" }
            val pullStartedAt = System.currentTimeMillis()
            pullImage(UTILS_IMAGE).start().awaitCompletion(60, TimeUnit.SECONDS)
            log.info { "Pull completed in ${System.currentTimeMillis() - pullStartedAt}ms" }
        }

        actionStatus.progress = 0.1f

        var targetZipFile = targetFile
        if (!targetZipFile.name.endsWith(".zip")) {
            targetZipFile = targetZipFile.parent.resolve(targetFile.name + ".zip")
        }
        val dirToExport = targetFile.parent.resolve(targetZipFile.name + "_files")
        dirToExport.toFile().mkdirs()

        try {

            actionStatus.message = "Validate volumes"

            val volumesByName = volumes.associateBy {
                val origName = it.labels[DockerLabels.ORIGINAL_NAME]
                if (origName.isNullOrBlank()) {
                    error("Original name of ${it.name} is missing")
                }
                origName
            }

            val progressForVolume = (0.9f - actionStatus.progress) / volumes.size

            val volumesSnapMeta = ArrayList<VolumeSnapshotMeta>()

            for ((originalName, volume) in volumesByName) {

                actionStatus.message = "Create backup for '$originalName'"

                log.info { "Create backup of volume '$originalName'..." }
                val volumeBackupStartedAt = System.currentTimeMillis()

                val dataFile = FileUtils.sanitizeFileName(originalName) + ".tar.${alg.extension}"
                volumesSnapMeta.add(VolumeSnapshotMeta(originalName, dataFile))

                val container: CreateContainerResponse = client.createContainerCmd(UTILS_IMAGE)
                    .withCmd(
                        "/bin/sh", "-c", "cd /source && " +
                            "find . -mindepth 1 -printf '%P\\n' | " +
                            "tar $tarAlgParam -cvf \"/dest/${dataFile}\" -T -"
                    )
                    .withHostConfig(
                        HostConfig.newHostConfig()
                            .withBinds(
                                Bind.parse(volume.name + ":/source"),
                                Bind.parse("${dirToExport.absolutePathString()}:/dest"),
                            )
                    ).withLabels(
                        mapOf(
                            DockerLabels.LAUNCHER_LABEL_PAIR
                        )
                    ).exec()

                client.startContainerCmd(container.id).exec()
                val statusCode = client.waitContainerCmd(container.id).start().awaitStatusCode(1, TimeUnit.MINUTES)
                if (statusCode == 0) {
                    log.info {
                        "Backup successfully created for $originalName " +
                        "in ${System.currentTimeMillis() - volumeBackupStartedAt}ms"
                    }
                } else {
                    log.error { "===== Backup completed with non-zero status code: $statusCode. Container logs: =====" }
                    consumeLogs(container.id, 100_000, Duration.ofSeconds(10)) { msg -> log.warn { msg } }
                    log.error { "===== End of backup container logs =====" }
                }
                removeContainer(container.id)

                actionStatus.addProgress(progressForVolume)
            }

            val namespaceSnapshotMeta = NamespaceSnapshotMeta(volumesSnapMeta, Instant.now())
            Json.writePretty(dirToExport.resolve("meta.json"), namespaceSnapshotMeta)

            actionStatus.set("Create snapshot archive", 0.95f)

            ZipUtils.createZip(dirToExport, targetZipFile)

            actionStatus.set("", 1f)

        } finally {
            try {
                dirToExport.toFile().deleteRecursively()
            } catch (e: Throwable) {
                log.error(e) { "Export dir deletion failed: ${dirToExport.absolutePathString()}" }
            }
        }
        log.info {
            "Snapshot created successfully for " +
            "namespace $nsRef in ${System.currentTimeMillis() - exportStartedAt}ms"
        }
    }

    fun createBridgeNetwork(nsRef: NamespaceRef, name: String): CreateNetworkResponse {
        return client.createNetworkCmd()
            .withLabels(mapOf(
                DockerLabels.WORKSPACE to nsRef.workspace,
                DockerLabels.NAMESPACE to nsRef.namespace,
                DockerLabels.LAUNCHER_LABEL_PAIR
            ))
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
