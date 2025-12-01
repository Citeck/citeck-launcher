@file:Suppress("unused")

package ru.citeck.launcher.core.namespace.runtime.docker

import com.github.dockerjava.api.DockerClient
import com.github.dockerjava.api.async.ResultCallback
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
import com.github.dockerjava.api.model.Statistics
import com.github.dockerjava.transport.DockerHttpClient
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.namespace.NamespaceRef
import ru.citeck.launcher.core.namespace.runtime.ContainerStats
import ru.citeck.launcher.core.namespace.runtime.DockerSystemInfo
import ru.citeck.launcher.core.namespace.runtime.docker.exception.DockerStaleNetworkException
import ru.citeck.launcher.core.snapshot.NamespaceSnapshotMeta
import ru.citeck.launcher.core.snapshot.VolumeSnapshotMeta
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.CompressionAlg
import ru.citeck.launcher.core.utils.ZipUtils
import ru.citeck.launcher.core.utils.file.FileUtils
import ru.citeck.launcher.core.utils.json.Json
import java.nio.file.Path
import java.time.Duration
import java.time.Instant
import java.util.concurrent.TimeUnit
import java.util.zip.ZipFile
import kotlin.io.path.*

class DockerApi(
    private val client: DockerClient,
    private val httpClient: DockerHttpClient
) {
    companion object {
        private val log = KotlinLogging.logger {}
        const val UTILS_IMAGE = "registry.citeck.ru/community/launcher-utils:1.0"

        private const val GRACEFUL_SHUTDOWN_SECONDS = 10
        private const val SYSTEM_INFO_CACHE_TTL_MS = 120_000L
    }

    @Volatile
    private var cachedSystemInfo: DockerSystemInfo? = null

    @Volatile
    private var systemInfoCacheTime: Long = 0

    fun createVolume(nsRef: NamespaceRef, originalName: String, name: String): CreateVolumeResponse {
        return client.createVolumeCmd()
            .withLabels(
                mapOf(
                    DockerLabels.WORKSPACE to nsRef.workspace,
                    DockerLabels.NAMESPACE to nsRef.namespace,
                    DockerLabels.ORIGINAL_NAME to originalName,
                    DockerLabels.LAUNCHER_LABEL_PAIR,
                    DockerLabels.DOCKER_COMPOSE_PROJECT to DockerConstants.getDockerProjectName(nsRef)
                )
            ).withName(name).exec()
    }

    // +++ VOLUMES +++

    fun getVolumesSize(): Map<String, Long> {

        val request = DockerHttpClient.Request.builder()
            .method(DockerHttpClient.Request.Method.GET)
            .path("/system/df?type=volume")
            .build()

        return try {
            httpClient.execute(request).use { response ->
                val body = response.body ?: return emptyMap()
                val jsonResp = Json.readJson(body.readAllBytes())
                val result = mutableMapOf<String, Long>()
                jsonResp["Volumes"]?.forEach { volume ->
                    val name = volume["Name"]?.asText()
                    val size = volume["UsageData"]?.get("Size")?.asLong()
                    if (name != null && size != null) {
                        result[name] = size
                    }
                }
                result
            }
        } catch (e: Throwable) {
            log.error(e) { "getVolumesSize call failed" }
            emptyMap()
        }
    }

    fun getVolumeByNameOrNull(name: String): InspectVolumeResponse? {
        return client.listVolumesCmd()
            .withFilter("name", listOf(name)).exec()
            .volumes.firstOrNull()
    }

    fun getVolumeByOriginalNameOrNull(nsRef: NamespaceRef, name: String): InspectVolumeResponse? {
        return client.listVolumesCmd()
            .withFilter(
                "label",
                listOf(
                    DockerLabels.NAMESPACE + "=" + nsRef.namespace,
                    DockerLabels.ORIGINAL_NAME + "=" + name
                )
            ).exec().volumes.firstOrNull {
                nsRef.workspace.equals(it.labels[DockerLabels.WORKSPACE], true)
            }
    }

    fun getVolumes(nsRef: NamespaceRef?): List<InspectVolumeResponse> {
        nsRef ?: return emptyList()
        return client.listVolumesCmd()
            .withFilter(
                "label",
                listOf(
                    DockerLabels.NAMESPACE + "=" + nsRef.namespace
                )
            ).exec().volumes.filter {
                nsRef.workspace.equals(it.labels[DockerLabels.WORKSPACE], true)
            }
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
                    DockerLabels.NAMESPACE to nsRef.namespace
                )
            ).exec().filter {
                nsRef.workspace.equals(it.labels[DockerLabels.WORKSPACE], true)
            }
    }

    fun getContainerByNameOrNull(name: String): Container? {
        if (name.isBlank()) {
            return null
        }
        return client.listContainersCmd()
            .withShowAll(true)
            .withNameFilter(listOf(name))
            .exec()
            .firstOrNull()
    }

    fun getContainers(nsRef: NamespaceRef, appName: String): List<Container> {
        return client.listContainersCmd()
            .withShowAll(true)
            .withLabelFilter(
                mapOf(
                    DockerLabels.NAMESPACE to nsRef.namespace,
                    DockerLabels.APP_NAME to appName
                )
            ).exec().filter {
                nsRef.workspace.equals(it.labels[DockerLabels.WORKSPACE], true)
            }
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
            "Stop and remove container $containerId. " +
                "Name: $containerName State: $containerState"
        }
        try {
            try {
                client.stopContainerCmd(containerId).withTimeout(GRACEFUL_SHUTDOWN_SECONDS).exec()
            } catch (_: NotFoundException) {
                // do nothing
            }
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
        removeContainer(containerId)
    }

    fun removeContainer(containerId: String) {
        try {
            client.removeContainerCmd(containerId)
                .withForce(true)
                .withRemoveVolumes(true)
                .exec()
        } catch (_: NotFoundException) {
            // do nothing
        }
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
        } catch (_: NotFoundException) {
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
        try {
            client.removeNetworkCmd(network.id).exec()
        } catch (e: DockerException) {
            if ((e.message ?: "").contains("has active endpoints") && network.containers.isEmpty()) {
                throw DockerStaleNetworkException(e)
            } else {
                throw e
            }
        }
    }

    fun validateSnapshot(
        snapshot: Path
    ): NamespaceSnapshotMeta {
        return ZipFile(snapshot.toFile()).use { zip ->
            val metaEntry = zip.getEntry("meta.json")
                ?: error("Invalid snapshot: ${snapshot.absolutePathString()}. meta.json is not present")
            val snapshotMeta = zip.getInputStream(metaEntry).use { input ->
                Json.read(input, NamespaceSnapshotMeta::class)
            }
            if (snapshotMeta.volumes.isEmpty()) {
                error("Snapshot with empty volumes: ${snapshot.absolute()}")
            }
            snapshotMeta
        }
    }

    fun importSnapshot(
        nsRef: NamespaceRef,
        snapshot: Path,
        actionStatus: ActionStatus.Mut,
        namePrefix: String = ""
    ): List<String> {
        log.info { "Snapshot importing started. Namespace: $nsRef Snapshot: $snapshot" }
        if (!snapshot.exists()) {
            error("Snapshot file does not exist: ${snapshot.absolutePathString()}")
        }
        checkUtilsImage(actionStatus)
        val importedVolumes = mutableListOf<String>()
        ZipFile(snapshot.toFile()).use { zip ->
            actionStatus.set("Read meta", 0.01f)
            val metaEntry = zip.getEntry("meta.json")
                ?: error("Invalid snapshot: ${snapshot.absolutePathString()}. meta.json is not present")
            val snapshotMeta = zip.getInputStream(metaEntry).use { input ->
                Json.read(input, NamespaceSnapshotMeta::class)
            }
            log.info { "Snapshot meta: ${Json.toString(snapshotMeta)}" }
            val outDir = snapshot.parent.resolve(snapshot.toFile().name + "_import")
            outDir.toFile().mkdir()
            try {
                actionStatus.set("Check volumes", 0.05f)
                for (volume in snapshotMeta.volumes) {
                    val volumeNameInNs = DockerConstants.getVolumeName(namePrefix, volume.name, nsRef)
                    if (getVolumeByNameOrNull(volumeNameInNs) != null) {
                        error("Volume $volumeNameInNs already exists")
                    }
                }
                actionStatus.set("Import volumes data", 0.1f)
                val progressForOneVolume = 0.9f / snapshotMeta.volumes.size
                for (volume in snapshotMeta.volumes) {
                    actionStatus.message = "Import '${volume.name}'"
                    val dataEntry = zip.getEntry(volume.dataFile)
                    if (dataEntry == null) {
                        log.warn { "Volume listed in snapshot meta, but doesn't exists in archive: ${volume.dataFile}" }
                        actionStatus.addProgress(progressForOneVolume)
                        continue
                    }
                    log.info { "Extract ${volume.dataFile} to temp dir" }
                    val extractedVolumeDataFile = outDir.resolve(volume.dataFile)
                    zip.getInputStream(dataEntry).use { volumeDataStream ->
                        extractedVolumeDataFile.outputStream().use { fileOut ->
                            volumeDataStream.copyTo(fileOut)
                        }
                    }
                    val volumeNameInNs = DockerConstants.getVolumeName(namePrefix, volume.name, nsRef)
                    createVolume(nsRef, volume.name, volumeNameInNs)
                    importedVolumes.add(volumeNameInNs)

                    val srcArchive = "/source/${volume.dataFile}"
                    var command = "tar -xf $srcArchive -C /dest"
                    val rootStat = volume.rootStat
                    if (rootStat.isNotBlank()) {
                        val rootStatParts = rootStat.split("|")
                        command = "chown ${rootStatParts[0]} /dest " +
                            "&& chmod ${rootStatParts[1]} /dest && " + command
                    }
                    execWithUtils(
                        nsRef,
                        command,
                        listOf(
                            "$volumeNameInNs:/dest",
                            "${extractedVolumeDataFile.absolutePathString()}:$srcArchive"
                        )
                    )

                    actionStatus.addProgress(progressForOneVolume)

                    extractedVolumeDataFile.deleteExisting()
                }
            } finally {
                outDir.toFile().deleteRecursively()
            }
        }
        return importedVolumes
    }

    private fun checkUtilsImage(status: ActionStatus.Mut) {
        if (inspectImageOrNull(UTILS_IMAGE) != null) {
            return
        }
        status.message = "Pull utils image"
        log.info { "Image $UTILS_IMAGE is not exists locally. Let's pull it" }
        val pullStartedAt = System.currentTimeMillis()
        pullImage(UTILS_IMAGE).start().awaitCompletion(60, TimeUnit.SECONDS)
        log.info { "Pull completed in ${System.currentTimeMillis() - pullStartedAt}ms" }
        status.message = "Utils pull completed"
    }

    private fun execWithUtils(nsRef: NamespaceRef, command: String, volumes: List<String>) {
        log.info { "Execute utils command: $command" }
        val execStartedAt = System.currentTimeMillis()
        val container: CreateContainerResponse = client.createContainerCmd(UTILS_IMAGE)
            .withCmd("/bin/sh", "-c", command)
            .withHostConfig(
                HostConfig.newHostConfig()
                    .withBinds(volumes.map { Bind.parse(it) })
            ).withLabels(
                mapOf(
                    DockerLabels.WORKSPACE to nsRef.workspace,
                    DockerLabels.NAMESPACE to nsRef.namespace,
                    DockerLabels.LAUNCHER_LABEL_PAIR,
                    DockerLabels.DOCKER_COMPOSE_PROJECT to DockerConstants.getDockerProjectName(nsRef)
                )
            ).exec()

        client.startContainerCmd(container.id).exec()
        val statusCode = client.waitContainerCmd(container.id).start().awaitStatusCode(1, TimeUnit.MINUTES)
        if (statusCode == 0) {
            log.info {
                "Command successfully completed " +
                    "in ${System.currentTimeMillis() - execStartedAt}ms"
            }
        } else {
            log.error { "===== Command completed with non-zero status code: $statusCode. Container logs: =====" }
            consumeLogs(container.id, 100_000, Duration.ofSeconds(10)) { msg -> log.warn { msg } }
            log.error { "===== End of command container logs =====" }
        }
        removeContainer(container.id)
    }

    fun exportSnapshot(
        nsRef: NamespaceRef,
        targetFile: Path,
        alg: CompressionAlg,
        actionStatus: ActionStatus.Mut
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

        checkUtilsImage(actionStatus)

        actionStatus.progress = 0.1f

        var targetZipFile = targetFile
        if (!targetZipFile.name.endsWith(".zip")) {
            targetZipFile = targetZipFile.parent.resolve(targetFile.name + ".zip")
        }
        if (targetZipFile.exists()) {
            var newNameCounter = 1
            val baseName = targetFile.name.substringBeforeLast('.')
            while (targetZipFile.exists()) {
                targetZipFile = targetZipFile.parent.resolve(baseName + "_" + (newNameCounter++) + ".zip")
            }
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

                actionStatus.message = "Create snapshot of '$originalName'"

                log.info { "Create snapshot of volume '$originalName'..." }

                val dataFile = FileUtils.sanitizeFileName(originalName) + ".tar.${alg.extension}"

                execWithUtils(
                    nsRef,
                    "cd /source && " +
                        "find . -mindepth 1 -printf '%P\\n' | " +
                        "tar $tarAlgParam -cvf \"/dest/${dataFile}\" -T - && " +
                        "stat -c \"%u:%g|0%a\" /source > \"/dest/$dataFile.stat\"",
                    listOf(
                        volume.name + ":/source",
                        "${dirToExport.absolutePathString()}:/dest"
                    )
                )
                val statFile = dirToExport.resolve("$dataFile.stat")
                volumesSnapMeta.add(
                    VolumeSnapshotMeta(
                        originalName,
                        statFile.readText(Charsets.UTF_8).trim(),
                        dataFile
                    )
                )
                statFile.deleteIfExists()

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
            .withLabels(
                mapOf(
                    DockerLabels.WORKSPACE to nsRef.workspace,
                    DockerLabels.NAMESPACE to nsRef.namespace,
                    DockerLabels.LAUNCHER_LABEL_PAIR,
                    DockerLabels.DOCKER_COMPOSE_PROJECT to DockerConstants.getDockerProjectName(nsRef)
                )
            )
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

    fun getSystemInfo(): DockerSystemInfo {
        val now = System.currentTimeMillis()
        val cached = cachedSystemInfo
        if (cached != null && now - systemInfoCacheTime < SYSTEM_INFO_CACHE_TTL_MS) {
            return cached
        }

        return try {
            val info = client.infoCmd().exec()
            val result = DockerSystemInfo(
                cpuCores = info.ncpu ?: 0,
                memoryTotal = info.memTotal ?: 0L
            )
            cachedSystemInfo = result
            systemInfoCacheTime = now
            result
        } catch (e: Exception) {
            log.warn(e) { "Failed to get Docker system info" }
            cached ?: DockerSystemInfo.EMPTY
        }
    }

    fun watchContainerStats(
        containerId: String,
        onStats: (ContainerStats) -> Unit,
        onError: (Throwable) -> Unit = {}
    ): AutoCloseable {
        val resultCallback = object : ResultCallback.Adapter<Statistics>() {
            override fun onNext(stats: Statistics) {
                onStats(calculateContainerStats(stats))
            }
            override fun onError(throwable: Throwable) {
                if (throwable !is java.io.IOException) {
                    log.warn(throwable) { "Stats stream error for container $containerId" }
                }
                onError(throwable)
            }
        }

        client.statsCmd(containerId)
            .withNoStream(false)
            .exec(resultCallback)

        return AutoCloseable {
            try {
                resultCallback.close()
            } catch (_: Exception) {}
        }
    }

    private fun calculateContainerStats(stats: Statistics): ContainerStats {
        val cpuStats = stats.cpuStats
        val preCpuStats = stats.preCpuStats

        val cpuDelta = (cpuStats?.cpuUsage?.totalUsage ?: 0L) - (preCpuStats?.cpuUsage?.totalUsage ?: 0L)
        val systemDelta = (cpuStats?.systemCpuUsage ?: 0L) - (preCpuStats?.systemCpuUsage ?: 0L)
        val cpuCount = cpuStats?.onlineCpus
            ?: cpuStats?.cpuUsage?.percpuUsage?.size?.toLong()
            ?: 1L

        val cpuPercent = if (systemDelta > 0 && cpuDelta > 0) {
            (cpuDelta.toDouble() / systemDelta.toDouble()) * cpuCount * 100.0
        } else {
            0.0
        }

        // CPU throttling info
        val throttlingData = cpuStats?.throttlingData
        val cpuThrottledPeriods = throttlingData?.throttledPeriods ?: 0L
        val cpuThrottledTime = throttlingData?.throttledTime ?: 0L

        val memStats = stats.memoryStats
        val memStatsDetails = memStats?.stats
        // Docker stats CLI formula: usage - inactive_file
        // cgroup v1: total_inactive_file, cgroup v2: inactive_file, fallback: cache
        val inactiveFile = memStatsDetails?.totalInactiveFile
            ?: memStatsDetails?.inactiveFile
            ?: memStatsDetails?.cache
            ?: 0L
        val memoryUsage = maxOf(0L, (memStats?.usage ?: 0L) - inactiveFile)
        val memoryLimit = memStats?.limit ?: 0L
        val memoryCache = memStatsDetails?.cache ?: 0L
        val memoryPercent = if (memoryLimit > 0) {
            (memoryUsage.toDouble() / memoryLimit.toDouble()) * 100.0
        } else {
            0.0
        }

        return ContainerStats(
            cpuPercent = cpuPercent,
            cpuCores = cpuCount,
            cpuThrottledPeriods = cpuThrottledPeriods,
            cpuThrottledTime = cpuThrottledTime,
            memoryUsage = memoryUsage,
            memoryLimit = memoryLimit,
            memoryPercent = memoryPercent,
            memoryCache = memoryCache
        )
    }
}
