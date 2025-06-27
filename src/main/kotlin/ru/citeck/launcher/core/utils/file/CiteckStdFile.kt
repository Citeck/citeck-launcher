package ru.citeck.launcher.core.utils.file

import java.io.File
import java.io.InputStream
import java.io.OutputStream
import java.net.URI
import kotlin.io.path.relativeTo

class CiteckStdFile(
    private val file: File
) : AbstractCiteckFile() {

    override fun getUri(): URI {
        return file.toURI()
    }

    override fun getName(): String {
        return file.name
    }

    override fun isFile(): Boolean {
        return file.isFile
    }

    override fun isDirectory(): Boolean {
        return file.isDirectory
    }

    override fun <T> read(action: (InputStream) -> T): T {
        return file.inputStream().use(action)
    }

    override fun <T> write(action: (OutputStream) -> T): T {
        return file.outputStream().use(action)
    }

    override fun createFile(name: String, writeAction: (OutputStream) -> Unit): CiteckStdFile {
        val childFile = File(file, name)
        childFile.outputStream().use(writeAction)
        return CiteckStdFile(childFile)
    }

    override fun getChildren(): List<CiteckFile> {
        return file.listFiles()?.map { CiteckStdFile(it) } ?: emptyList()
    }

    override fun getFilesContent(): Map<String, ByteArray> {
        val result = mutableMapOf<String, ByteArray>()
        val basePath = file.toPath()
        for (file in file.walkTopDown()) {
            if (file.isFile) {
                result[file.toPath()
                    .relativeTo(basePath)
                    .toString()
                    .replace("\\", "/")] = file.readBytes()
            }
        }
        return result
    }

    override fun getOrCreateDir(name: String): CiteckFile {
        val dirFile = File(file, name)
        dirFile.mkdirs()
        return CiteckStdFile(dirFile)
    }

    override fun getFile(name: String): CiteckFile {
        return CiteckStdFile(File(file, name))
    }
}
