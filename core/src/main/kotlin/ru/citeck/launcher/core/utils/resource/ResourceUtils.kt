package ru.citeck.launcher.core.utils.resource

import java.io.File
import java.io.FileOutputStream
import java.net.*
import java.util.*
import java.util.jar.JarFile

object ResourceUtils {

    /**
     * URL prefix for loading from the class path
     */
    const val CLASSPATH_URL_PREFIX = "classpath:"

    /**
     * URL protocol for a file in the file system: "file"
     */
    const val URL_PROTOCOL_FILE = "file"

    fun copyFiles(fromLocation: URL, toLocation: File) {

        fun copyJarResourcesRecursively(destination: File, jarConnection: JarURLConnection) {
            val jarFile: JarFile = jarConnection.jarFile
            val jarEntries = jarFile.entries()
            while (jarEntries.hasMoreElements()) {
                val entry = jarEntries.nextElement()
                if (!entry.name.startsWith(jarConnection.entryName)) {
                    continue
                }
                val fileName: String = entry.name.removePrefix(jarConnection.entryName)
                if (!entry.isDirectory) {
                    jarFile.getInputStream(entry).use { entryStream ->
                        FileOutputStream(File(destination, fileName)).use { fileOut ->
                            entryStream.copyTo(fileOut)
                        }
                    }
                } else {
                    File(destination, fileName).mkdirs()
                }
            }
        }

        fun copyResourcesRecursively(originUrl: URL, destination: File) {

            when (val urlConnection: URLConnection = originUrl.openConnection()) {
                is JarURLConnection -> copyJarResourcesRecursively(destination, urlConnection)
                else -> File(originUrl.path).copyRecursively(destination, overwrite = true)
            }
        }

        copyResourcesRecursively(fromLocation, toLocation)
    }

    fun copyFiles(fromLocation: String, toLocation: File) {
        copyFiles(getUrl(fromLocation), toLocation)
    }

    /**
     * Resolve the given resource location to a `java.net.URL` or null if resource is not found
     */
    fun getUrlOrNull(location: String?): URL? {
        if (location.isNullOrBlank()) {
            return null
        }
        return try {
            getUrl(location)
        } catch (e: ResourceNotFoundException) {
            null
        }
    }

    /**
     * Resolve the given resource location to a `java.net.URL`
     *
     * @throws ResourceNotFoundException
     */
    fun getUrl(location: String): URL {

        Objects.requireNonNull(location, "Resource location must not be null")

        if (location.startsWith(CLASSPATH_URL_PREFIX)) {
            val path: String = location.substring(CLASSPATH_URL_PREFIX.length)
            val classLoader: ClassLoader? = getDefaultClassLoader()
            val url = if (classLoader != null) {
                classLoader.getResource(path)
            } else {
                ClassLoader.getSystemResource(path)
            }
            if (url == null) {
                throw ResourceNotFoundException(
                    "class path resource [$path] " +
                        "cannot be resolved to URL because it does not exist"
                )
            }
            return url
        }

        return try {
            URI(location).toURL()
        } catch (ex: MalformedURLException) {
            // invalid URL. Maybe it is a simple file location?
            try {
                File(location).toURI().toURL()
            } catch (ex2: MalformedURLException) {
                throw ResourceNotFoundException("Invalid resource location: $location")
            }
        }
    }

    /**
     * Resolve the given resource location to a `java.io.File`
     *
     * @throws ResourceNotFoundException
     */
    fun getFile(location: String): File {
        return getFile(getUrl(location))
    }

    /**
     * Resolve the given resource location to a `java.io.File` or null if file won't be found
     */
    fun getFileOrNull(location: String?): File? {
        if (location.isNullOrBlank()) {
            return null
        }
        return try {
            getFile(location)
        } catch (e: ResourceNotFoundException) {
            null
        }
    }

    /**
     * Resolve the given resource URL to a `java.io.File` or null if file w
     */
    fun getFileOrNull(resourceUrl: URL?): File? {
        resourceUrl ?: return null
        return try {
            getFile(resourceUrl)
        } catch (e: ResourceNotFoundException) {
            null
        }
    }

    /**
     * Resolve the given resource URL to a `java.io.File`
     *
     * @throws ResourceNotFoundException
     */
    fun getFile(resourceUrl: URL): File {

        Objects.requireNonNull(resourceUrl, "Resource URL must not be null")

        if (URL_PROTOCOL_FILE != resourceUrl.protocol) {
            throw ResourceNotFoundException("\"$resourceUrl\" cannot be resolved to absolute file path")
        }
        val file = try {
            File(URI(resourceUrl.toString()).schemeSpecificPart)
        } catch (ex: URISyntaxException) {
            File(resourceUrl.file)
        }
        if (!file.exists()) {
            throw ResourceNotFoundException("File doesn't exists by path \"${file.absolutePath}\"")
        }
        return file
    }

    private fun getDefaultClassLoader(): ClassLoader? {

        var classLoader: ClassLoader? = null
        try {
            classLoader = Thread.currentThread().contextClassLoader
        } catch (e: Throwable) {
            // Class loader from thread is not accessible
        }
        if (classLoader == null) {
            // Let's try to use class loader of this class.
            classLoader = ResourceUtils::class.java.classLoader
            if (classLoader == null) {
                // ok, let's try to return system ClassLoader
                try {
                    classLoader = ClassLoader.getSystemClassLoader()
                } catch (e: Throwable) {
                    // do nothing
                }
            }
        }
        return classLoader
    }
}
