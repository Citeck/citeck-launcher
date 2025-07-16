package ru.citeck.launcher.core.snapshot

import io.github.oshai.kotlinlogging.KotlinLogging
import io.ktor.client.*
import io.ktor.client.engine.cio.*
import io.ktor.client.plugins.HttpTimeout
import io.ktor.client.request.*
import io.ktor.client.statement.*
import io.ktor.http.*
import io.ktor.utils.io.*
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.file.FileUtils
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.startForPromise
import ru.citeck.launcher.core.workspace.WorkspacesService
import java.io.File
import java.io.RandomAccessFile
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.io.path.exists
import kotlin.io.path.name
import kotlin.math.min

class WorkspaceSnapshots {

    companion object {
        private const val REPEATS_LIMIT_WITHOUT_PROGRESS = 3
        private const val REPEATS_LIMIT_TOTAL = 100
        private const val REPEAT_DELAY_MS = 3000L

        private const val DOWNLOAD_PART_BYTES = 10 * 1024 * 1024L // 10mb

        private val log = KotlinLogging.logger {}
    }

    private lateinit var workspaceServices: WorkspaceServices

    fun init(workspaceServices: WorkspaceServices) {
        this.workspaceServices = workspaceServices
    }

    fun getSnapshot(snapshotId: String, status: ActionStatus.Mut): Promise<Path> {

        val completed = AtomicBoolean(false)
        val downloadProgress = DownloadProgress(0.0)
        val contentLength = ContentLength(0)
        val statusUpdater = Thread.ofVirtual().name("download-status-updater").start {
            while (!completed.get()) {
                try {
                    status.progress = downloadProgress.value.toFloat()
                    Thread.sleep(1000)
                } catch (_: InterruptedException) {
                    // do nothing
                }
            }
        }

        var totalRepeats = 0
        return Thread.ofPlatform().name("snapshot-loader").startForPromise(completed) { completed ->
            var repeats = REPEATS_LIMIT_WITHOUT_PROGRESS
            val firstIteration = AtomicBoolean(true)
            do {
                if (!firstIteration.compareAndSet(true, false)) {
                    Thread.sleep(REPEAT_DELAY_MS)
                }
                val progressBefore: Double = downloadProgress.value
                try {
                    status.message = "Snapshot downloading..."
                    return@startForPromise runBlocking {
                        resolveSnapshot(snapshotId, downloadProgress, contentLength)
                    }
                } catch (e: Throwable) {
                    status.message = "Downloading error\nWill try again in ${REPEAT_DELAY_MS / 1000}s"
                    log.error(e) { "Exception while loading snapshot '$snapshotId'" }
                    if (downloadProgress.value > progressBefore) {
                        repeats = REPEATS_LIMIT_WITHOUT_PROGRESS + 1
                    }
                }
            } while (!completed.get() && ++totalRepeats < REPEATS_LIMIT_TOTAL && --repeats > 0)
            error(
                "Snapshot downloading failed after $totalRepeats repeats. " +
                    "Total bytes: ${contentLength.value} Progress: ${downloadProgress.value}"
            )
        }.finally {
            statusUpdater.interrupt()
        }
    }

    private suspend fun resolveSnapshot(
        snapshotId: String,
        downloadProgress: DownloadProgress,
        contentLength: ContentLength
    ): Path {

        val snapshotInfo = workspaceServices.workspaceConfig
            .getValue()
            .snapshots
            .find { it.id == snapshotId } ?: error("Snapshot not found: '$snapshotId'")

        val snapshotFile = WorkspacesService.getWorkspaceDir(workspaceServices.workspace.id)
            .resolve("snapshots")
            .resolve("$snapshotId.zip")

        if (snapshotFile.exists()) {

            val actualHash = FileUtils.getFileSha256(snapshotFile)

            if (actualHash == snapshotInfo.sha256) {

                log.info { "Using existing snapshot: ${snapshotFile.fileName}, hash verified." }
                return snapshotFile
            } else {

                val baseName = snapshotFile.fileName
                    .toString()
                    .substringBeforeLast('.') +
                    "_outdated_" +
                    FileUtils.createNameWithCurrentDateTime()

                var newPath = snapshotFile.parent.resolve("$baseName.zip")
                var renameIteration = 0
                while (newPath.exists()) {
                    newPath = snapshotFile.parent.resolve("${baseName}_${++renameIteration}.zip")
                }

                log.info {
                    "Obsolete snapshot detected: ${snapshotFile.fileName}. " +
                        "Hash mismatch (expected: ${snapshotInfo.sha256}, actual: $actualHash). " +
                        "Rename outdated file to ${newPath.fileName}."
                }
                Files.move(snapshotFile, newPath)
            }
        } else {
            log.info {
                "Snapshot file not found, " +
                    "initiating download: ${snapshotFile.fileName} from url ${snapshotInfo.url}"
            }
        }

        downloadFileImpl(
            snapshotInfo.url,
            snapshotFile,
            downloadProgress,
            contentLength
        )

        val actualHash = FileUtils.getFileSha256(snapshotFile)
        if (actualHash != snapshotInfo.sha256) {
            error(
                "Snapshot was downloaded successfully, but hash mismatch " +
                    "(expected: ${snapshotInfo.sha256}, actual: $actualHash)." +
                    "Please, contact with mantainers and report this problem."
            )
        }

        return snapshotFile
    }

    private suspend fun downloadFileImpl(
        url: String,
        targetFile: Path,
        downloadProgress: DownloadProgress,
        contentLength: ContentLength
    ) {
        val partFile = targetFile.parent.resolve(targetFile.name + ".part").toFile()
        if (!partFile.exists()) {
            partFile.parentFile.mkdirs()
            partFile.createNewFile()
        }
        HttpClient(CIO) {
            install(HttpTimeout) {
                requestTimeoutMillis = Duration.ofMinutes(6).toMillis()
                connectTimeoutMillis = Duration.ofSeconds(10).toMillis()
                socketTimeoutMillis = Duration.ofMinutes(5).toMillis()
            }
        }.use { client ->
            downloadFileImpl(client, url, partFile, downloadProgress, contentLength)
        }
        Files.move(partFile.toPath(), targetFile)
    }

    private suspend fun downloadFileImpl(
        client: HttpClient,
        url: String,
        targetFile: File,
        downloadProgress: DownloadProgress,
        contentLength: ContentLength
    ) {
        contentLength.value = client.head(url).contentLength() ?: error("Can't get content length")

        var downloadedBytes = if (targetFile.exists()) targetFile.length() else 0L
        downloadProgress.value = downloadedBytes / contentLength.toDouble()

        while (downloadedBytes < contentLength.value) {

            val rangeEnd = min(downloadedBytes + DOWNLOAD_PART_BYTES, contentLength.value)

            client.prepareGet(url) {
                header("Range", "bytes=$downloadedBytes-$rangeEnd")
            }.execute { response ->
                if (response.status.value in listOf(200, 206)) {
                    val channel: ByteReadChannel = response.bodyAsChannel()
                    RandomAccessFile(targetFile, "rw").use { file ->
                        file.seek(downloadedBytes)
                        val buffer = ByteArray(8192)
                        while (!channel.isClosedForRead) {
                            val bytesRead = channel.readAvailable(buffer)
                            if (bytesRead > 0) {
                                file.write(buffer, 0, bytesRead)
                                downloadedBytes += bytesRead
                                downloadProgress.value = downloadedBytes / contentLength.toDouble()
                            }
                        }
                    }
                } else {
                    error("Unexpected response code: ${response.status}")
                }
            }
        }
    }

    private class DownloadProgress(@Volatile var value: Double)
    private class ContentLength(@Volatile var value: Long) {
        fun toDouble() = value.toDouble()
    }
}
