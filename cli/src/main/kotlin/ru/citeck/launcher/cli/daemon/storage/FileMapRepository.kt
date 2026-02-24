package ru.citeck.launcher.cli.daemon.storage

import com.fasterxml.jackson.databind.JavaType
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.database.Repository
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.core.utils.json.Yaml
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.util.concurrent.ConcurrentHashMap
import kotlin.io.path.exists
import kotlin.io.path.readText
import kotlin.io.path.writeText

class FileMapRepository<T : Any>(
    private val file: Path,
    private val valueType: JavaType
) : Repository<String, T> {

    companion object {
        private val log = KotlinLogging.logger {}
    }

    private val data = ConcurrentHashMap<String, T>()

    init {
        loadFromFile()
    }

    @Suppress("UNCHECKED_CAST")
    private fun loadFromFile() {
        if (file.exists()) {
            try {
                val content = file.readText()
                if (content.isNotBlank()) {
                    val loaded = Yaml.read(content, Map::class)
                    (loaded as? Map<String, Any>)?.forEach { (k, v) ->
                        data[k] = Json.convert(v, valueType) as T
                    }
                }
            } catch (e: Throwable) {
                log.warn(e) { "Failed to read data file: $file. Starting with empty state." }
            }
        }
    }

    @Synchronized
    private fun flush() {
        file.parent?.toFile()?.mkdirs()
        val snapshot = HashMap<String, Any>()
        data.forEach { (k, v) -> snapshot[k] = Json.convertToStringAnyMap(v as Any) }
        val tmpFile = file.resolveSibling(file.fileName.toString() + ".tmp")
        tmpFile.writeText(Yaml.toString(snapshot))
        Files.move(tmpFile, file, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
    }

    override fun set(id: String, value: T) {
        data[id] = value
        flush()
    }

    override fun get(id: String): T? {
        return data[id]
    }

    override fun delete(id: String) {
        data.remove(id)
        flush()
    }

    override fun find(max: Int): List<T> {
        return data.values.take(max)
    }

    override fun getFirst(): T? {
        return data.values.firstOrNull()
    }

    override fun forEach(action: (String, T) -> Boolean) {
        for ((k, v) in data) {
            if (action(k, v)) {
                break
            }
        }
    }
}
