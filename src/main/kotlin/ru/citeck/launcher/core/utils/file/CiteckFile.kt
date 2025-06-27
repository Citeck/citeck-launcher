package ru.citeck.launcher.core.utils.file

import java.io.InputStream
import java.io.OutputStream
import java.net.URI

interface CiteckFile {

    fun getUri(): URI

    fun getName(): String

    fun isFile(): Boolean

    fun isDirectory(): Boolean

    fun readBytes(): ByteArray

    fun <T> read(action: (InputStream) -> T): T

    fun <T> write(action: (OutputStream) -> T): T

    fun createFile(name: String, writeAction: (OutputStream) -> Unit): CiteckFile

    fun copyTo(dest: CiteckFile, filter: (CiteckFile) -> Boolean = { true })

    fun getChildren(): List<CiteckFile>

    fun getFilesContent(): Map<String, ByteArray>

    fun getFile(name: String): CiteckFile

    fun getOrCreateDir(name: String): CiteckFile
}
