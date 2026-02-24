package ru.citeck.launcher.cli.daemon.storage

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.database.DataRepo
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.core.utils.json.Yaml
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.util.concurrent.ConcurrentHashMap
import kotlin.io.path.exists
import kotlin.io.path.readText
import kotlin.io.path.writeText

class FileDataRepo(
    private val file: Path
) : DataRepo {

    companion object {
        private val log = KotlinLogging.logger {}
    }

    private val data = ConcurrentHashMap<String, DataValue>()

    init {
        loadFromFile()
    }

    private fun loadFromFile() {
        if (file.exists()) {
            try {
                val content = file.readText()
                if (content.isNotBlank()) {
                    val loaded = Yaml.read(content, Map::class)
                    @Suppress("UNCHECKED_CAST")
                    (loaded as? Map<String, Any>)?.forEach { (k, v) ->
                        data[k] = DataValue.create(v)
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
        val snapshot = HashMap<String, Any?>()
        data.forEach { (k, v) -> snapshot[k] = Json.convert(v.asJson(), Any::class) }
        val tmpFile = file.resolveSibling(file.fileName.toString() + ".tmp")
        tmpFile.writeText(Yaml.toString(snapshot))
        Files.move(tmpFile, file, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
    }

    override fun set(id: String, value: Any) {
        data[id] = DataValue.of(value)
        flush()
    }

    override fun set(id: String, value: DataValue) {
        data[id] = value
        flush()
    }

    override fun get(id: String): DataValue {
        return data[id] ?: DataValue.NULL
    }

    override fun delete(id: String) {
        data.remove(id)
        flush()
    }

    override fun find(max: Int): List<DataValue> {
        return data.values.take(max)
    }

    override fun getFirst(): DataValue? {
        return data.values.firstOrNull()
    }

    override fun forEach(action: (String, DataValue) -> Boolean) {
        for ((k, v) in data) {
            if (action(k, v)) {
                break
            }
        }
    }
}
