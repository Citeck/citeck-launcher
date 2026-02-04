package ru.citeck.launcher.core.utils.file

import ru.citeck.launcher.core.utils.resource.ResourceUtils
import java.io.File
import java.net.JarURLConnection
import java.net.URI
import java.net.URL
import java.nio.file.Path

object CiteckFiles {

    fun getFile(path: Path): CiteckFile {
        return CiteckStdFile(path.toFile())
    }

    fun getFile(uri: URI): CiteckFile {
        return getFile(uri.toURL())
    }

    fun getFile(path: String): CiteckFile {
        return getFile(ResourceUtils.getUrl(path))
    }

    fun getFile(url: URL): CiteckFile {
        try {
            val conn = url.openConnection()
            return if (conn is JarURLConnection) {
                val fileName = url.path
                    .substringAfterLast('/')
                    .substringAfterLast('\\')
                CiteckJarFile(url, !fileName.contains('.'))
            } else {
                CiteckStdFile(File(url.toURI()))
            }
        } catch (e: Exception) {
            throw RuntimeException("File can't be resolved for url $url", e)
        }
    }
}
