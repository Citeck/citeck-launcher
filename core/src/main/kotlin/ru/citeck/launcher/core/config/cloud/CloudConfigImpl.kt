package ru.citeck.launcher.core.config.cloud

import ru.citeck.launcher.core.utils.json.Json
import java.util.*
import kotlin.collections.LinkedHashMap

class CloudConfigImpl : MutableCloudConfig {

    private val config = LinkedHashMap<ConfigKey, Map<String, Any?>>()

    override fun getConfig(appName: String, profiles: Collection<String>): Map<String, Any?> {
        if (config.isEmpty()) {
            return emptyMap()
        }
        val result = LinkedHashMap<String, Any?>()
        this.config[ConfigKey(appName, "")]?.let { result.putAll(it) }
        for (profile in profiles) {
            this.config[ConfigKey(appName, profile)]?.let { result.putAll(it) }
        }
        return result
    }

    override fun put(appName: String, config: Any) {
        put(appName, "", config)
    }

    override fun put(appName: String, profile: String, config: Any) {
        val configMap = Json.convertToStringAnyMap(config)
        val flatConfig = LinkedHashMap<String, Any?>()
        buildFlattenedMap(flatConfig, configMap)
        this.config[ConfigKey(appName, profile)] = flatConfig
    }

    private fun buildFlattenedMap(result: MutableMap<String, Any?>, source: Map<*, *>, path: String? = null) {
        for ((srcKey, value) in source) {
            if (srcKey !is String) {
                continue
            }
            var key: String = srcKey
            if (!path.isNullOrBlank()) {
                key = if (key.startsWith("[")) {
                    path + key
                } else {
                    "$path.$key"
                }
            }
            if (value is String) {
                result[key] = value
            } else if (value is Map<*, *>) {
                @Suppress("UNCHECKED_CAST")
                buildFlattenedMap(result, value as Map<String, Any>, key)
            } else if (value is Collection<*>) {
                @Suppress("UNCHECKED_CAST")
                val collection = value as Collection<Any>
                if (collection.isEmpty()) {
                    result[key] = ""
                } else {
                    for ((idx, obj) in collection.withIndex()) {
                        buildFlattenedMap(result, Collections.singletonMap("[$idx]", obj), key)
                    }
                }
            } else {
                result[key] = value
            }
        }
    }

    private data class ConfigKey(
        val appName: String,
        val profile: String
    )
}
