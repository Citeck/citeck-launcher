package ru.citeck.launcher.cli.daemon.services

import io.github.oshai.kotlinlogging.KotlinLogging
import io.ktor.client.*
import io.ktor.client.engine.cio.*
import io.ktor.client.plugins.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import io.ktor.http.*
import io.ktor.utils.io.*
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.cli.daemon.storage.ConfigPaths
import ru.citeck.launcher.cli.daemon.storage.FileDataRepo
import ru.citeck.launcher.cli.daemon.storage.FileRepository
import ru.citeck.launcher.core.bundle.BundleRef
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.namespace.NamespaceRef
import ru.citeck.launcher.core.namespace.runtime.NamespaceRuntime
import ru.citeck.launcher.core.namespace.runtime.NsRuntimeFiles
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.file.FileUtils
import ru.citeck.launcher.core.utils.json.Yaml
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.io.RandomAccessFile
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.io.path.exists
import kotlin.io.path.name
import kotlin.io.path.readText
import kotlin.io.path.writeText
import kotlin.math.min

class NamespaceConfigManager(
    private val daemonServices: DaemonServices,
    private val workspaceContext: DaemonWorkspaceContext
) : Disposable {

    companion object {
        private val log = KotlinLogging.logger {}
        private const val DEFAULT_NS_ID = "default"
    }

    private var runtime: NamespaceRuntime? = null

    fun load(): Boolean {
        val configFile = ConfigPaths.NAMESPACE_CONFIG
        if (!configFile.exists()) {
            log.info { "Namespace config not found: $configFile" }
            return false
        }
        try {
            loadNamespace(configFile)
            return true
        } catch (e: Throwable) {
            log.error(e) { "Failed to load namespace config from $configFile" }
            return false
        }
    }

    private fun loadNamespace(file: Path) {
        val nsConfig = Yaml.read(file, NamespaceConfig::class)
        val id = nsConfig.id.ifBlank { DEFAULT_NS_ID }
        log.info { "Loaded namespace config: $id from $file" }

        val builder = nsConfig.copy().withId(id)

        // Resolve bundle reference
        var bundleRef = builder.bundleRef
        if (bundleRef.isEmpty()) {
            val wsConfig = workspaceContext.workspaceConfig.getValue()
            val repoId = wsConfig.bundleRepos.firstOrNull()?.id ?: "community"
            bundleRef = BundleRef.create(repoId, "LATEST")
        }
        if (bundleRef.key == "LATEST") {
            val resolved = daemonServices.bundlesService.getLatestRepoBundle(bundleRef.repo)
            if (!resolved.isEmpty()) {
                bundleRef = resolved
            }
        }
        builder.withBundleRef(bundleRef)
        log.info { "Using bundle: $bundleRef" }

        val finalConfig = builder.build()
        importSnapshotIfNeeded(finalConfig)
        registerRuntime(finalConfig)
    }

    private fun importSnapshotIfNeeded(nsConfig: NamespaceConfig) {
        val snapshotId = nsConfig.snapshot
        if (snapshotId.isBlank()) return

        val markerFile = ConfigPaths.SNAPSHOTS_DIR.resolve("imported-${nsConfig.id}")
        if (markerFile.exists() && markerFile.readText().trim() == snapshotId) {
            log.info { "Snapshot '$snapshotId' already imported for namespace '${nsConfig.id}', skipping" }
            return
        }

        val wsConfig = workspaceContext.workspaceConfig.getValue()
        val snapshotInfo = wsConfig.snapshots.find { it.id == snapshotId }
        if (snapshotInfo == null) {
            log.error { "Snapshot '$snapshotId' not found in workspace config" }
            return
        }

        try {
            log.info { "Importing snapshot '${snapshotInfo.name}' (${snapshotInfo.size}) for namespace '${nsConfig.id}'" }

            val snapshotFile = downloadSnapshot(snapshotInfo)

            val namespaceRef = NamespaceRef(workspaceContext.workspace.id, nsConfig.id)
            val status = ActionStatus.Mut()
            daemonServices.dockerApi.importSnapshot(namespaceRef, snapshotFile, status)

            markerFile.writeText(snapshotId)
            log.info { "Snapshot '$snapshotId' imported successfully" }
        } catch (e: Throwable) {
            log.error(e) { "Failed to import snapshot '$snapshotId'" }
        }
    }

    private fun downloadSnapshot(snapshotInfo: WorkspaceConfig.Snapshot): Path {
        val targetFile = ConfigPaths.SNAPSHOTS_DIR.resolve("${snapshotInfo.id}.zip")

        if (targetFile.exists()) {
            val actualHash = FileUtils.getFileSha256(targetFile)
            if (actualHash == snapshotInfo.sha256) {
                log.info { "Snapshot file already cached: $targetFile" }
                return targetFile
            }
            log.info { "Cached snapshot hash mismatch, re-downloading (expected: ${snapshotInfo.sha256}, actual: $actualHash)" }
        }

        val partFile = targetFile.parent.resolve(targetFile.name + ".part")
        log.info { "Downloading snapshot from ${snapshotInfo.url}" }

        runBlocking {
            HttpClient(CIO) {
                install(HttpTimeout) {
                    requestTimeoutMillis = Duration.ofMinutes(30).toMillis()
                    connectTimeoutMillis = Duration.ofSeconds(30).toMillis()
                    socketTimeoutMillis = Duration.ofMinutes(10).toMillis()
                }
            }.use { client ->
                downloadWithResume(client, snapshotInfo.url, partFile)
            }
        }
        Files.move(partFile, targetFile, java.nio.file.StandardCopyOption.REPLACE_EXISTING)

        val actualHash = FileUtils.getFileSha256(targetFile)
        if (actualHash != snapshotInfo.sha256) {
            Files.deleteIfExists(targetFile)
            error(
                "Snapshot hash mismatch after download " +
                    "(expected: ${snapshotInfo.sha256}, actual: $actualHash)"
            )
        }

        return targetFile
    }

    private suspend fun downloadWithResume(client: HttpClient, url: String, targetFile: Path) {
        if (!targetFile.exists()) {
            targetFile.parent?.toFile()?.mkdirs()
            targetFile.toFile().createNewFile()
        }

        val contentLength = client.head(url).contentLength()
            ?: error("Cannot determine content length for $url")

        var downloadedBytes = targetFile.toFile().length()

        val partSize = 10 * 1024 * 1024L
        while (downloadedBytes < contentLength) {
            val rangeEnd = min(downloadedBytes + partSize - 1, contentLength - 1)
            log.info { "Downloading ${downloadedBytes / (1024 * 1024)}MB / ${contentLength / (1024 * 1024)}MB" }

            client.prepareGet(url) {
                header("Range", "bytes=$downloadedBytes-$rangeEnd")
            }.execute { response ->
                val channel: ByteReadChannel = response.bodyAsChannel()
                RandomAccessFile(targetFile.toFile(), "rw").use { file ->
                    if (response.status.value == HttpStatusCode.OK.value) {
                        // Server ignores Range — restart from beginning
                        file.seek(0)
                        downloadedBytes = 0
                    } else if (response.status.value == HttpStatusCode.PartialContent.value) {
                        file.seek(downloadedBytes)
                    } else {
                        error("Unexpected response: ${response.status}")
                    }
                    val buffer = ByteArray(8192)
                    while (!channel.isClosedForRead) {
                        val bytesRead = channel.readAvailable(buffer)
                        if (bytesRead > 0) {
                            file.write(buffer, 0, bytesRead)
                            downloadedBytes += bytesRead
                        }
                    }
                }
            }
        }
    }

    private fun registerRuntime(nsConfig: NamespaceConfig) {
        val existing = runtime
        if (existing != null) {
            existing.namespaceConfig.setValue(nsConfig)
            log.info { "Updated namespace runtime: ${nsConfig.id}" }
            return
        }

        val namespaceRef = NamespaceRef(workspaceContext.workspace.id, nsConfig.id)

        val runtimeFiles = NsRuntimeFiles(
            namespaceRef,
            FileRepository(ConfigPaths.RUNTIME_FILES_DIR)
        )

        runtime = NamespaceRuntime(
            namespaceRef,
            MutProp(nsConfig),
            workspaceContext.workspaceConfig,
            runtimeFiles,
            daemonServices.nsAppsGenerator,
            daemonServices.bundlesService,
            daemonServices.actionsService,
            daemonServices.dockerApi,
            FileDataRepo(ConfigPaths.RUNTIME_STATE_FILE),
            daemonServices.cloudConfigServer,
            volumesBaseDir = ConfigPaths.VOLUMES_DIR
        )
        log.info { "Registered namespace runtime: ${nsConfig.id}" }
    }

    fun getRuntime(): NamespaceRuntime? = runtime

    fun getConfig(): NamespaceConfig? = runtime?.namespaceConfig?.getValue()

    fun isConfigured(): Boolean = runtime != null

    fun reload(): Boolean {
        log.info { "Reloading configuration..." }
        val newWsConfig = daemonServices.reloadWorkspaceConfig()
        workspaceContext.workspaceConfig.setValue(newWsConfig)
        return load()
    }

    override fun dispose() {
        runtime?.dispose()
        runtime = null
    }
}
