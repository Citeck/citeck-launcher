package ru.citeck.launcher.core.utils.json

import org.snakeyaml.engine.v2.api.Dump
import org.snakeyaml.engine.v2.api.DumpSettings
import org.snakeyaml.engine.v2.api.Load
import org.snakeyaml.engine.v2.api.LoadSettings
import org.snakeyaml.engine.v2.common.FlowStyle
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
