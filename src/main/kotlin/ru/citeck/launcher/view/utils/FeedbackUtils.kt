package ru.citeck.launcher.view.utils

import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.logs.AppLogUtils
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json
import java.awt.Desktop
import java.io.File
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import java.nio.file.attribute.BasicFileAttributes
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import javax.swing.JFileChooser
import javax.swing.filechooser.FileNameExtensionFilter
import kotlin.io.path.exists
import kotlin.io.path.inputStream
import kotlin.io.path.outputStream
import kotlin.io.path.visitFileTree

object FeedbackUtils {

    private var services: LauncherServices? = null

    fun init(services: LauncherServices) {
        this.services = services
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

        runtimeInfo["totalMemoryMb"] = totalMemory / (1024f * 1024f)
        runtimeInfo["freeMemoryMb"] = freeMemory / (1024f * 1024f)
        runtimeInfo["usedMemoryMb"] = usedMemory / (1024f * 1024f)

        val availableProcessors = runtime.availableProcessors()
        runtimeInfo["availableProcessors"] = availableProcessors

        sysInfo["runtime"] = runtimeInfo

        return sysInfo
    }

    fun exportSystemInfo() {

        val homePath = Paths.get(System.getProperty("user.home"))
        val defaultExportDir = homePath.resolve("Downloads")

        val fileChooser = JFileChooser()
        fileChooser.fileFilter = FileNameExtensionFilter("ZIP files", "zip")
        fileChooser.dialogTitle = "Export System Info"
        fileChooser.fileSelectionMode = JFileChooser.FILES_ONLY

        if (defaultExportDir.exists()) {

            var defaultExportFileName = "launcher-sysinfo-"
            defaultExportFileName += Instant.now()
                .truncatedTo(ChronoUnit.MINUTES)
                .toString()
                .replace("Z", "")
                .replace("T", "_")
                .replace("[^\\d_]".toRegex(), "")
            defaultExportFileName += ".zip"

            fileChooser.selectedFile = defaultExportDir.resolve(defaultExportFileName).toFile()
        }

        val userSelection = fileChooser.showSaveDialog(null)
        if (userSelection == JFileChooser.APPROVE_OPTION) {
            val fileToSave = fileChooser.selectedFile
            val finalFile = if (fileToSave.extension != "zip") {
                File(fileToSave.parentFile, "${fileToSave.name}.zip")
            } else {
                fileToSave
            }
            val outDir = Files.createTempDirectory("citeck-launcher-feedback")
            outDir.resolve("sysinfo.json").outputStream().use {
                Json.write(it, getSystemInfo())
            }
            try {
                val logsTargetPath = outDir.resolve("launcher-logs.txt")
                if (AppLogUtils.getAppLogFilePath().exists()) {
                    Files.copy(AppLogUtils.getAppLogFilePath(), logsTargetPath)
                } else {
                    logsTargetPath.toFile().writeText("NO LOGS")
                }
                saveZip(outDir, finalFile)
            } finally {
                outDir.toFile().deleteRecursively()
            }
            Desktop.getDesktop().open(finalFile.parentFile)
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
}
