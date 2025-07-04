package ru.citeck.launcher.view.utils

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.logs.AppLogUtils
import ru.citeck.launcher.core.namespace.runtime.docker.DockerLabels
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.file.CiteckFiles
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.view.dialog.GlobalLoadingDialog
import java.awt.Desktop
import java.io.File
import java.io.PrintWriter
import java.lang.management.LockInfo
import java.lang.management.ThreadInfo
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.BasicFileAttributes
import java.text.SimpleDateFormat
import java.time.Duration
import java.time.Instant
import java.util.*
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlin.io.path.exists
import kotlin.io.path.inputStream
import kotlin.io.path.outputStream
import kotlin.io.path.visitFileTree

object SystemDumpUtils {

    private val log = KotlinLogging.logger {}

    private var services: LauncherServices? = null

    fun init(services: LauncherServices) {
        this.services = services
    }

    fun dumpSystemInfo(basic: Boolean = false) {
        val closeLoadingDialog = GlobalLoadingDialog.show()
        Thread.ofPlatform().name("sys-info-dump").start {
            try {
                exportSystemInfoImpl(basic)
            } finally {
                closeLoadingDialog()
            }
        }
    }

    private fun exportSystemInfoImpl(basic: Boolean) {

        val timestamp = SimpleDateFormat("yy-MM-dd_HH-mm").format(Date.from(Instant.now()))
        val reportDir = AppDir.PATH.resolve("reports").resolve(timestamp)
        reportDir.toFile().mkdirs()

        val reportFileName = "launcher-dump_" +
            SimpleDateFormat("yy-MM-dd_HH-mm").format(Date.from(Instant.now())) +
            ".zip"

        val reportTargetFile = reportDir.resolve(reportFileName).toFile()

        val outDir = Files.createTempDirectory("citeck-launcher-dump")
        outDir.resolve("sysinfo.json").outputStream().use {
            Json.writePretty(it, getSystemInfo())
        }
        if (!basic) {
            exportNamespaceInfo(outDir.resolve("namespace"))
        }
        exportThreadDump(outDir)

        val logsTargetPath = outDir.resolve("launcher-logs.txt")
        if (AppLogUtils.getAppLogFilePath().exists()) {
            Files.copy(AppLogUtils.getAppLogFilePath(), logsTargetPath)
        } else {
            logsTargetPath.toFile().writeText("NO LOGS")
        }

        try {
            val buildInfoData = CiteckFiles.getFile("classpath:build-info.json").readBytes()
            outDir.resolve("build-info.json").outputStream().use { it.write(buildInfoData) }
        } catch (e: Throwable) {
            log.warn(e) { "No build info" }
        }

        try {
            saveZip(outDir, reportTargetFile)
        } finally {
            outDir.toFile().deleteRecursively()
        }

        Desktop.getDesktop().open(reportTargetFile.parentFile)
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

    private fun exportThreadDump(targetDir: Path) {
        try {
            targetDir.resolve("thread-dump.txt").outputStream().use { outStream ->
                PrintWriter(outStream).use { writer ->
                    try {
                        val managementFactoryClass = Class.forName("java.lang.management.ManagementFactory")
                        val threadMXBeanClass = Class.forName("java.lang.management.ThreadMXBean")

                        val threadMXBean = managementFactoryClass
                            .getMethod("getThreadMXBean")
                            .invoke(null)

                        val dumpMethod = threadMXBeanClass.getMethod(
                            "dumpAllThreads",
                            Boolean::class.javaPrimitiveType,
                            Boolean::class.javaPrimitiveType,
                            Int::class.javaPrimitiveType
                        )
                        val threadInfos = dumpMethod.invoke(threadMXBean, true, true, Int.MAX_VALUE) as Array<*>

                        threadInfos.forEach { threadInfo ->
                            writer.println(printStackTrace(threadInfo as ThreadInfo))
                        }
                    } catch (e: Throwable) {
                        e.printStackTrace(writer)
                    }
                }
            }
        } catch (e: Throwable) {
            log.error(e) { "Thread dump export error" }
        }
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

    // Implementation from ThreadInfo.toString, but without limit by depth
    private fun printStackTrace(threadInfo: ThreadInfo): String {

        val sb = StringBuilder(
            "\"" + threadInfo.threadName + "\"" +
                (if (threadInfo.isDaemon) " daemon" else "") +
                " prio=" + threadInfo.priority +
                " Id=" + threadInfo.threadId + " " +
                threadInfo.threadState
        )
        if (threadInfo.lockName != null) {
            sb.append(" on " + threadInfo.lockName)
        }
        if (threadInfo.lockOwnerName != null) {
            sb.append(
                " owned by \"" + threadInfo.lockOwnerName +
                    "\" Id=" + threadInfo.lockOwnerId
            )
        }
        if (threadInfo.isSuspended) {
            sb.append(" (suspended)")
        }
        if (threadInfo.isInNative) {
            sb.append(" (in native)")
        }
        sb.append('\n')
        var i = 0
        val stackTrace = threadInfo.stackTrace
        while (i < stackTrace.size) {
            val ste: StackTraceElement = stackTrace[i]!!
            sb.append("\tat $ste")
            sb.append('\n')
            if (i == 0 && threadInfo.lockInfo != null) {
                val ts: Thread.State = threadInfo.threadState
                when (ts) {
                    Thread.State.BLOCKED -> {
                        sb.append("\t-  blocked on " + threadInfo.lockInfo).append('\n')
                    }
                    Thread.State.WAITING -> {
                        sb.append("\t-  waiting on " + threadInfo.lockInfo).append('\n')
                    }
                    Thread.State.TIMED_WAITING -> {
                        sb.append("\t-  waiting on " + threadInfo.lockInfo).append('\n')
                    }
                    else -> {}
                }
            }

            for (mi in threadInfo.lockedMonitors) {
                if (mi.lockedStackDepth == i) {
                    sb.append("\t-  locked $mi")
                    sb.append('\n')
                }
            }
            i++
        }
        val locks: Array<LockInfo?> = threadInfo.lockedSynchronizers
        if (locks.isNotEmpty()) {
            sb.append("\n\tNumber of locked synchronizers = " + locks.size)
            sb.append('\n')
            for (li in locks) {
                sb.append("\t- $li")
                sb.append('\n')
            }
        }
        sb.append('\n')
        return sb.toString()
    }
}
