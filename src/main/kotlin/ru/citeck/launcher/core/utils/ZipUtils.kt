package ru.citeck.launcher.core.utils

import org.apache.commons.io.FilenameUtils
import java.io.File
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.BasicFileAttributes
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlin.io.path.inputStream
import kotlin.io.path.visitFileTree

object ZipUtils {

    fun createZip(sourceDir: Path, targetFile: Path) {

        targetFile.toFile().outputStream().use { fileOut ->
            ZipOutputStream(fileOut).use { zipOut ->
                sourceDir.visitFileTree {
                    onVisitFile { file, _ ->
                        val zipPath = sourceDir.relativize(file)
                        val entry = ZipEntry(FilenameUtils.separatorsToUnix(zipPath.toString()))

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
