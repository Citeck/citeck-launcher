package ru.citeck.launcher.cli.daemon.storage

import ru.citeck.launcher.core.database.Repository
import java.nio.file.Path
import kotlin.io.path.deleteIfExists
import kotlin.io.path.exists
import kotlin.io.path.listDirectoryEntries
import kotlin.io.path.name
import kotlin.io.path.readBytes
import kotlin.io.path.writeBytes

class FileRepository(
    private val dir: Path
) : Repository<String, ByteArray> {

    init {
        dir.toFile().mkdirs()
    }

    private fun keyToFile(id: String): Path {
        return dir.resolve(id.replace("/", "__"))
    }

    private fun fileToKey(file: Path): String {
        return file.name.replace("__", "/")
    }

    override fun set(id: String, value: ByteArray) {
        val file = keyToFile(id)
        file.parent?.toFile()?.mkdirs()
        file.writeBytes(value)
    }

    override fun get(id: String): ByteArray? {
        val file = keyToFile(id)
        return if (file.exists()) file.readBytes() else null
    }

    override fun delete(id: String) {
        keyToFile(id).deleteIfExists()
    }

    override fun find(max: Int): List<ByteArray> {
        if (!dir.exists()) return emptyList()
        return dir.listDirectoryEntries()
            .take(max)
            .filter { it.exists() }
            .map { it.readBytes() }
    }

    override fun getFirst(): ByteArray? {
        if (!dir.exists()) return null
        val first = dir.listDirectoryEntries().firstOrNull() ?: return null
        return first.readBytes()
    }

    override fun forEach(action: (String, ByteArray) -> Boolean) {
        if (!dir.exists()) return
        for (file in dir.listDirectoryEntries()) {
            if (action(fileToKey(file), file.readBytes())) {
                break
            }
        }
    }
}
