package ru.citeck.launcher.core.utils.json

import org.snakeyaml.engine.v2.api.Dump
import org.snakeyaml.engine.v2.api.DumpSettings
import org.snakeyaml.engine.v2.api.Load
import org.snakeyaml.engine.v2.api.LoadSettings
import org.snakeyaml.engine.v2.api.lowlevel.Compose
import org.snakeyaml.engine.v2.common.FlowStyle
import org.snakeyaml.engine.v2.nodes.Node
import java.io.File
import java.io.InputStream
import java.nio.file.Path
import kotlin.reflect.KClass

object Yaml {

    fun <T : Any> read(text: String, type: KClass<T>): T {
        val yamlLoad = Load(LoadSettings.builder().build())
        val value = yamlLoad.loadFromString(text)
        return Json.convert(value, type)
    }

    /**
     * Parses YAML through only the Parse+Compose stages — no `Construct` —
     * and hands back the raw `Node` tree: a scalar's `.value` is still the
     * literal source text (an unquoted `17.10` is NOT yet the `Double` 17.1),
     * the same guarantee Go's `yaml.Node` makes. `read` above cannot offer
     * this: it runs `Construct` (`Load.loadFromString`) before Jackson sees
     * anything, and that step is exactly where a trailing zero like `17.10`'s
     * is lost for good. Callers that need a field's exact text (see
     * `RawImageValues`) compose the same source text a second time here,
     * rather than trying to recover it from the already-lossy object `read`
     * produces.
     */
    fun composeNode(text: String): Node? {
        return Compose(LoadSettings.builder().build()).composeString(text).orElse(null)
    }

    fun <T : Any> read(file: Path, type: KClass<T>): T {
        return read(file.toFile(), type)
    }

    fun <T : Any> read(file: File, type: KClass<T>): T {
        val yamlLoad = Load(LoadSettings.builder().build())
        val value = file.inputStream().use { yamlLoad.loadFromInputStream(it) }
        return Json.convert(value, type)
    }

    fun <T : Any> read(input: InputStream, type: KClass<T>): T {
        val yamlLoad = Load(LoadSettings.builder().build())
        val value = yamlLoad.loadFromInputStream(input)
        return Json.convert(value, type)
    }

    fun toString(value: Any): String {
        val dump = Dump(
            DumpSettings.builder()
                .setExplicitStart(true)
                .setDefaultFlowStyle(FlowStyle.BLOCK)
                .setIndicatorIndent(2)
                .setIndent(2)
                .setIndentWithIndicator(true)
                .build()
        )
        val valueToDump = if (value !is Map<*, *> && value !is Collection<*>) {
            Json.convertToStringAnyMap(value)
        } else {
            value
        }
        return dump.dumpToString(valueToDump)
    }
}
