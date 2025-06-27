package ru.citeck.launcher.core.utils.file

import java.io.InputStream
import java.io.OutputStream
import java.net.JarURLConnection
import java.net.URI
import java.net.URL
import java.util.ArrayList
import java.util.jar.JarFile

class CiteckJarFile(
    private val url: URL,
    private val isDir: Boolean
) : AbstractCiteckFile() {

    companion object {
        private val SIMPLE_NAME_REGEX = "^[\\w.-]+/?$".toRegex()
    }

    private val name: String = url.path.substringAfterLast('/')

    override fun getUri(): URI {
        return url.toURI()
    }

    override fun getName(): String {
        return name
    }

    override fun isFile(): Boolean {
        return !isDir
    }

    override fun isDirectory(): Boolean {
        return isDir
    }

    override fun <T> read(action: (InputStream) -> T): T {
        if (isDir) {
            error("Directory can't be read. Url: $url")
        }
        return url.openStream().use(action)
    }

    override fun <T> write(action: (OutputStream) -> T): T {
        error("Unsupported")
    }

    override fun createFile(name: String, writeAction: (OutputStream) -> Unit): CiteckFile {
        error("Unsupported")
    }

    override fun getFile(name: String): CiteckFile {
        return CiteckJarFile(URI.create("$url/$name").toURL(), !name.contains("."))
    }

    override fun getFilesContent(): Map<String, ByteArray> {
        val conn = url.openConnection()
        if (conn !is JarURLConnection) {
            return emptyMap()
        }
        val prefixToRemove = conn.entryName + "/"
        val result = mutableMapOf<String, ByteArray>()
        val jarFile: JarFile = conn.jarFile
        val jarEntries = jarFile.entries()
        while (jarEntries.hasMoreElements()) {
            val entry = jarEntries.nextElement()
            if (!entry.name.startsWith(conn.entryName)) {
                continue
            }
            val fileName: String = entry.name.removePrefix(prefixToRemove)
            if (fileName.isBlank() || fileName.endsWith("/")) {
                continue
            }
            result[fileName] = URI.create("$url/$fileName").toURL().openStream().use { it.readBytes() }
        }
        return result
    }

    override fun getChildren(): List<CiteckFile> {
        val conn = url.openConnection()
        if (conn !is JarURLConnection) {
            return emptyList()
        }
        val prefixToRemove = conn.entryName + "/"
        val result = ArrayList<CiteckFile>()
        val jarFile: JarFile = conn.jarFile
        val jarEntries = jarFile.entries()
        while (jarEntries.hasMoreElements()) {
            val entry = jarEntries.nextElement()
            if (!entry.name.startsWith(conn.entryName)) {
                continue
            }
            var fileName: String = entry.name.removePrefix(prefixToRemove)
            var isDir = false
            if (fileName.endsWith("/")) {
                fileName = fileName.substring(0, fileName.length - 1)
                isDir = true
            }
            if (fileName.matches(SIMPLE_NAME_REGEX)) {
                result.add(CiteckJarFile(URI.create("$url/$fileName").toURL(), isDir))
            }
        }
        return result
    }

    override fun getOrCreateDir(name: String): CiteckFile {
        error("Unsupported")
    }
}
