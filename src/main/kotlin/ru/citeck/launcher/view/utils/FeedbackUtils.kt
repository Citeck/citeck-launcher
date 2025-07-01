package ru.citeck.launcher.view.utils

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.logs.AppLogUtils
import ru.citeck.launcher.core.namespace.runtime.docker.DockerLabels
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.view.dialog.GlobalLoadingDialog
import java.awt.Desktop
import java.io.File
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.BasicFileAttributes
import java.text.SimpleDateFormat
import java.time.Duration
import java.time.Instant
import java.util.Date
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlin.io.path.exists
import kotlin.io.path.inputStream
import kotlin.io.path.outputStream
import kotlin.io.path.visitFileTree

object FeedbackUtils {

    private val log = KotlinLogging.logger {}

    private var services: LauncherServices? = null

    fun init(services: LauncherServices) {
        this.services = services
    }

    fun dumpSystemInfo() {
        val closeLoadingDialog = GlobalLoadingDialog.show()
        Thread.ofPlatform().start {
            try {
                exportSystemInfoImpl()
            } finally {
                closeLoadingDialog()
            }
        }
    }

    private fun getSystemInfo(): DataValue {

        val sysInfo = DataValue.createObj()
        val systemProps = listOf(
            "java.version",
            "os.name",
            "os.version",
            "os.arch"
        )
        val sysProps = DataValue.createObj()
        for (prop in systemProps) {
            sysProps[prop] = System.getProperty(prop)
        }
        sysInfo["sysProps"] = sysProps

        val runtime = Runtime.getRuntime()

        val totalMemory = runtime.totalMemory()
        val freeMemory = runtime.freeMemory()
        val usedMemory = totalMemory - freeMemory

        val runtimeInfo = DataValue.createObj()

        val ramInfo = DataValue.createObj()

        ramInfo["totalMemoryMb"] = totalMemory / (1024f * 1024f)
        ramInfo["freeMemoryMb"] = freeMemory / (1024f * 1024f)
        ramInfo["usedMemoryMb"] = usedMemory / (1024f * 1024f)

        runtimeInfo["RAM"] = ramInfo

        val appDirRomInfo = DataValue.createObj()
        val appDirFile = AppDir.PATH.toFile()
        appDirRomInfo["totalSpace"] = appDirFile.totalSpace
        appDirRomInfo["freeSpace"] = appDirFile.freeSpace
        appDirRomInfo["usableSpace"] = appDirFile.usableSpace
        runtimeInfo["appDirROM"] = appDirRomInfo

        val availableProcessors = runtime.availableProcessors()
        runtimeInfo["availableProcessors"] = availableProcessors

        sysInfo["runtime"] = runtimeInfo
        sysInfo["time"] = Instant.now()

        return sysInfo
    }

    private fun exportNamespaceInfo(targetDir: Path) {
        val services = services ?: return

        if (!targetDir.exists()) {
            targetDir.toFile().mkdirs()
        }

        val workspace = services.getWorkspaceServices()
        val meta = DataValue.createObj()
        meta["workspaceId"] = workspace.workspace.id
        val selectedNs = workspace.selectedNamespace.value
        if (selectedNs == null) {
            meta["selectedNs"] = null
        } else {
            meta["selectedNs"] = selectedNs.id
            meta["bundleRef"] = selectedNs.bundleRef

            val nsRuntimeData = DataValue.createObj()
            val runtime = workspace.getCurrentNsRuntime()
            if (runtime != null) {

                nsRuntimeData["status"] = runtime.status.value
                val logsDir = targetDir.resolve("logs")
                logsDir.toFile().mkdirs()

                val containers = services.dockerApi.getContainers(runtime.namespaceRef)

                nsRuntimeData["containers"] = containers.map {
                    val containerInfo = DataValue.createObj()
                    containerInfo["id"] = it.id
                    val container = services.dockerApi.inspectContainerOrNull(it.id)
                    if (container != null) {
                        val labels = container.config?.labels ?: emptyMap()

                        containerInfo["name"] = container.name
                        containerInfo["state"] = container.state
                        containerInfo["labels"] = labels
                        containerInfo["image"] = container.config.image

                        val appName = labels[DockerLabels.APP_NAME] ?: "unknown"
                        try {
                            val logsFileName = appName + "_" + container.id.substring(0, 12) + ".log"
                            logsDir.resolve(logsFileName).outputStream().use { out ->
                                val lineBreak = "\n".toByteArray(Charsets.UTF_8)
                                services.dockerApi.consumeLogs(
                                    container.id,
                                    1_000_000,
                                    Duration.ofSeconds(10)
                                ) { logMsg ->
                                    out.write(LogsUtils.normalizeMessage(logMsg).toByteArray(Charsets.UTF_8))
                                    out.write(lineBreak)
                                }
                            }
                        } catch (e: Throwable) {
                            log.error(e) { "Error while logs consuming for $appName" }
                        }
                    }
                    containerInfo
                }
            }
            meta["nsRuntime"] = nsRuntimeData
        }
        targetDir.resolve("meta.json").outputStream().use {
            Json.writePretty(it, meta)
        }
    }

    private fun exportSystemInfoImpl() {

        val reportsDir = AppDir.PATH.resolve("reports")
        reportsDir.toFile().mkdir()

        val reportFileName = "launcher-dump_" +
            SimpleDateFormat("yy-MM-dd_HH-mm").format(Date.from(Instant.now())) +
            ".zip"

        val reportTargetFile = reportsDir.resolve(reportFileName).toFile()

        val outDir = Files.createTempDirectory("citeck-launcher-feedback")
        outDir.resolve("sysinfo.json").outputStream().use {
            Json.writePretty(it, getSystemInfo())
        }
        exportNamespaceInfo(outDir.resolve("namespace"))
        try {
            val logsTargetPath = outDir.resolve("launcher-logs.txt")
            if (AppLogUtils.getAppLogFilePath().exists()) {
                Files.copy(AppLogUtils.getAppLogFilePath(), logsTargetPath)
            } else {
                logsTargetPath.toFile().writeText("NO LOGS")
            }
            saveZip(outDir, reportTargetFile)
        } finally {
            outDir.toFile().deleteRecursively()
        }
        Desktop.getDesktop().open(reportTargetFile.parentFile)
    }

    private fun saveZip(sourceDir: Path, targetFile: File) {

        targetFile.outputStream().use { fileOut ->
            ZipOutputStream(fileOut).use { zipOut ->
                sourceDir.visitFileTree {
                    onVisitFile { file, _ ->
                        val zipPath = sourceDir.relativize(file)
                        val entry = ZipEntry(zipPath.toString())

                        val attrs = Files.readAttributes(file, BasicFileAttributes::class.java)
                        entry.lastModifiedTime = attrs.lastModifiedTime()
                        entry.creationTime = attrs.creationTime()
                        entry.lastAccessTime = attrs.lastAccessTime()

                        zipOut.putNextEntry(entry)
                        file.inputStream().use { it.copyTo(zipOut) }

                        zipOut.closeEntry()
                        FileVisitResult.CONTINUE
                    }
                }
            }
        }
    }
}
