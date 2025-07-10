package ru.citeck.launcher.core.utils.json

import com.fasterxml.jackson.core.JsonParseException
import com.fasterxml.jackson.core.type.TypeReference
import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.databind.JavaType
import com.fasterxml.jackson.databind.JsonNode
import com.fasterxml.jackson.databind.ObjectMapper
import com.fasterxml.jackson.databind.module.SimpleModule
import com.fasterxml.jackson.databind.node.ArrayNode
import com.fasterxml.jackson.databind.node.BinaryNode
import com.fasterxml.jackson.databind.node.MissingNode
import com.fasterxml.jackson.databind.node.NullNode
import com.fasterxml.jackson.databind.node.ObjectNode
import com.fasterxml.jackson.databind.node.TextNode
import com.fasterxml.jackson.module.kotlin.KotlinModule
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.entity.EntityRef
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.StringUtils
import ru.citeck.launcher.core.utils.bean.BeanUtils
import ru.citeck.launcher.core.utils.bean.PropertyDesc
import ru.citeck.launcher.core.utils.json.serialization.*
import java.io.*
import java.lang.reflect.ParameterizedType
import java.nio.file.Path
import java.time.Duration
import java.time.Instant
import java.util.*
import java.util.concurrent.ConcurrentHashMap
import kotlin.io.path.inputStream
import kotlin.io.path.outputStream
import kotlin.reflect.KClass

object Json {

    val mapper = ObjectMapper()

    private val log = KotlinLogging.logger {}

    private val defaultValueByClass = ConcurrentHashMap<KClass<*>, ObjectNode>()

    init {
        mapper.registerModule(KotlinModule.Builder().build())
        mapper.disable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES)

        val customModule = SimpleModule("citeck-launcher")
        customModule.addSerializer(InstantSerializer())
        customModule.addDeserializer(Instant::class.java, InstantDeserializer())
        customModule.addDeserializer(EntityRef::class.java, EntityRefDeserializer())
        customModule.addSerializer(DurationSerializer())
        customModule.addDeserializer(Duration::class.java, DurationDeserializer())

        mapper.registerModules(customModule)
    }

    /**
     * Determines whether the provided value can be read.
     * This method is useful to avoid unnecessary reading attempts.
     *
     * @param value the value to check for readability
     * @return true if the value can be read, false if the value is not readable.
     *         Note: If this method returns true, it indicates that the value may be readable,
     *         but it is not always guaranteed.
     */
    fun isReadableValue(value: Any?): Boolean {
        if (value == null) {
            return false
        }
        var valueToRead = value
        if (valueToRead is DataValue) {
            valueToRead = valueToRead.value
        }
        if (valueToRead is TextNode) {
            valueToRead = valueToRead.textValue()
        }
        if (valueToRead is BinaryNode) {
            valueToRead = valueToRead.binaryValue()
        }
        if (valueToRead is String) {
            val firstNonEmptyChar = valueToRead.firstOrNull { !Character.isWhitespace(it) } ?: return false
            if (firstNonEmptyChar == '{' || firstNonEmptyChar == '[') {
                return true
            }
            if (firstNonEmptyChar == '-') {
                val firstCharIdx = valueToRead.indexOf('-')
                if (valueToRead.length <= firstCharIdx + 4) {
                    return false
                }
                val yamlPrefix = valueToRead.substring(firstCharIdx, firstCharIdx + 4)
                if (yamlPrefix == "---\n") {
                    return true
                }
            }
        } else if (valueToRead is ByteArray && valueToRead.isNotEmpty() && valueToRead[0].toInt() == '{'.code) {
            return true
        }
        return false
    }

    fun newObjectNode(): ObjectNode {
        return mapper.createObjectNode()
    }

    fun newArrayNode(): ArrayNode {
        return mapper.createArrayNode()
    }

    fun toNonDefaultJsonObj(value: Any): ObjectNode {
        val json = toJsonObj(value)
        return toNonDefaultJson(json, value::class, null) as ObjectNode
    }

    private fun toNonDefaultJson(value: JsonNode, type: KClass<*>?, propDesc: PropertyDesc?): JsonNode {

        if (type == null) {
            return value
        }
        if (value.isArray) {
            if (propDesc == null) {
                return value
            }
            val returnType = propDesc.getReadMethod()?.genericReturnType
            if (returnType !is ParameterizedType) {
                return value
            }
            if (returnType.actualTypeArguments.size != 1) {
                return value
            }
            val elementType = returnType.actualTypeArguments[0]
            if (elementType !is KClass<*>) {
                return value
            }
            val resultNode = newArrayNode()
            for (node in value) {
                resultNode.add(toNonDefaultJson(node, elementType, null))
            }
            return resultNode
        }
        if (!value.isObject) {
            return value
        }

        val fieldTypes = if (Map::class.java.isAssignableFrom(type.java) ||
            Collection::class.java.isAssignableFrom(type.java) ||
            type.java.isArray
        ) {
            emptyMap()
        } else {
            BeanUtils.getProperties(type).associateBy { it.getName() }
        }

        val defaultValue = getDefaultValueByClass(type)

        val resultData = newObjectNode()
        val keys = value.fieldNames()
        while (keys.hasNext()) {
            val key = keys.next()
            val dataValue = value.get(key)
            if (dataValue != defaultValue.get(key)) {
                val propDescriptor = fieldTypes[key]
                resultData.set<JsonNode>(
                    key,
                    toNonDefaultJson(dataValue, propDescriptor?.getPropClass(), propDescriptor)
                )
            }
        }
        return resultData
    }

    private fun getDefaultValueByClass(clazz: KClass<*>): ObjectNode {
        return defaultValueByClass.computeIfAbsent(clazz) {
            convert(read("{}", it), ObjectNode::class)
        }
    }

    fun toJson(value: Any?): JsonNode {
        return mapper.convertValue(value, JsonNode::class.java)
    }

    fun toJsonObj(value: Any?): ObjectNode {
        return mapper.convertValue(value, ObjectNode::class.java)
    }

    fun <K : Any, V : Any> readMap(value: ByteArray, keyType: KClass<K>, valueType: KClass<V>): Map<K, V> {
        val mapType = mapper.typeFactory.constructMapType(Map::class.java, keyType.java, valueType.java)
        return mapper.readValue(value, mapType)
    }

    fun <T : Any> read(value: String, type: KClass<T>): T {
        return mapper.readValue(value, type.java)
    }

    fun <T : Any> read(value: Path, type: KClass<T>): T {
        return read(value.toFile(), type)
    }

    fun <T : Any> read(value: File, type: KClass<T>): T {
        return mapper.readValue(value, type.java)
    }

    fun <T : Any> read(value: InputStream, type: KClass<T>): T {
        return mapper.readValue(value, type.java)
    }

    fun <T : Any> read(value: ByteArray, type: JavaType): T {
        return mapper.readValue(value, type)
    }

    fun <T : Any> read(value: ByteArray, type: KClass<T>): T {
        return mapper.readValue(value, type.java)
    }

    fun readJson(input: Path): JsonNode {
        return input.inputStream().use { readJson(it) }
    }

    fun readJson(input: File): JsonNode {
        return input.inputStream().use { readJson(it) }
    }

    fun readJson(input: InputStream): JsonNode {
        return mapper.readTree(input)
    }

    fun readJson(value: String): JsonNode {
        return mapper.readTree(value)
    }

    fun readJson(value: ByteArray): JsonNode {
        return mapper.readTree(value)
    }

    fun writePretty(out: Path, value: Any?) {
        out.outputStream().use { writePretty(it, value) }
    }

    fun writePretty(out: File, value: Any?) {
        out.outputStream().use { writePretty(it, value) }
    }

    fun writePretty(out: OutputStream, value: Any?) {
        val valueToWrite = value ?: NullNode.instance
        mapper.writerWithDefaultPrettyPrinter().writeValue(out, valueToWrite)
    }

    fun write(out: OutputStream, value: Any?) {
        val valueToWrite = value ?: NullNode.instance
        mapper.writeValue(out, valueToWrite)
    }

    fun toBytes(value: Any?): ByteArray {
        return toString(value).toByteArray()
    }

    fun toString(value: Any?): String {
        if (value == null) {
            return "null"
        }
        return mapper.writeValueAsString(value)
    }

    fun getSimpleType(type: KClass<*>): JavaType {
        return mapper.typeFactory.constructType(type.java)
    }

    fun getSetType(elementType: KClass<*>): JavaType {
        return mapper.typeFactory.constructCollectionType(Set::class.java, elementType.java)
    }

    fun getListType(elementType: KClass<*>): JavaType {
        return mapper.typeFactory.constructCollectionType(List::class.java, elementType.java)
    }

    fun getMapType(keyType: KClass<*>, valueType: KClass<*>): JavaType {
        return mapper.typeFactory.constructMapType(Map::class.java, keyType.java, valueType.java)
    }

    fun getConcurrentMapType(keyType: KClass<*>, valueType: KClass<*>): JavaType {
        return mapper.typeFactory.constructMapType(ConcurrentHashMap::class.java, keyType.java, valueType.java)
    }

    fun <T : Any> convert(value: Any, type: JavaType): T {
        return mapper.convertValue(value, type)
    }

    fun <T : Any> convertOrNull(value: Any, type: KClass<T>): T? {
        return convertOrNull(value, mapper.constructType(type.java))
    }

    fun <T : Any> convertOrNull(value: Any?, type: JavaType): T? {
        value ?: return null
        if (value is DataValue && value.isNull()) {
            return null
        }
        if (value is JsonNode && value is NullNode || value is MissingNode) {
            return null
        }
        return try {
            convert(value, type)
        } catch (e: Throwable) {
            return null
        }
    }

    fun <T : Any> convert(value: Any, type: KClass<T>): T {
        return convert<T>(value, mapper.constructType(type.java), null, true)!!
    }

    fun <T : Any> convert(value: Any?, type: KClass<T>, orElse: T?): T? {
        if (value == null || value is DataValue && value.isNull() || value is NullNode || value is MissingNode) {
            return orElse
        }
        try {
            return mapper.convertValue(value, type.java)
        } catch (e: Throwable) {
            log.debug { "Exception while value converting: '${e.message}'. orElse value will be returned" }
            return orElse
        }
    }

    fun convertToStringAnyMap(value: Any): Map<String, Any> {
        return mapper.convertValue(value, object : TypeReference<Map<String, Any>>() {})
    }

    private fun <T : Any> convert(value: Any?, type: JavaType, deflt: T?, notNull: Boolean): T? {

        if (type.rawClass == Unit::class.java) {
            @Suppress("UNCHECKED_CAST")
            return Unit as T
        }

        var valueToConvert = value
        if (valueToConvert is Optional<*>) {
            valueToConvert = valueToConvert.orElse(null)
        }
        if (valueToConvert == null || "null" == valueToConvert) {
            if (notNull) {
                throw JsonMapperException("value is null", value, type)
            }
            return deflt
        }

        try {

            val result = if (isNull(valueToConvert)) {
                if (notNull) {
                    throw JsonMapperException("value is null", value, type)
                }
                deflt
            } else {
                if (type.rawClass == valueToConvert::class.java) {
                    valueToConvert
                } else {
                    if (valueToConvert is DataValue) {
                        valueToConvert = valueToConvert.asJson()
                    }
                    if (type.rawClass == DataValue::class.java) {
                        DataValue.create(valueToConvert)
                    } else if (
                        type.rawClass == JsonNode::class.java && valueToConvert is String
                    ) {
                        TextNode.valueOf(valueToConvert)
                    } else if (type.rawClass == JsonNode::class.java && valueToConvert is ByteArray) {
                        BinaryNode.valueOf(valueToConvert)
                    } else if (type.rawClass == JsonNode::class.java && valueToConvert is JsonNode) {
                        valueToConvert
                    } else if (type.rawClass == String::class.java &&
                        valueToConvert is JsonNode &&
                        valueToConvert.isTextual
                    ) {
                        valueToConvert.asText()
                    } else {

                        when (valueToConvert) {
                            is ByteArray -> {
                                var baResult: T? = null
                                if (valueToConvert.isNotEmpty() && valueToConvert[0].toInt() == '{'.code) {
                                    try {
                                        baResult = readNotNull(valueToConvert, type)
                                    } catch (e: JsonMapperException) {
                                        // do nothing
                                    }
                                }
                                if (baResult == null) {
                                    @Suppress("UNCHECKED_CAST")
                                    baResult = (
                                        when (type.rawClass) {
                                            JsonNode::class.java -> BinaryNode.valueOf(valueToConvert)
                                            DataValue::class.java -> DataValue.createAsIs(BinaryNode.valueOf(valueToConvert))
                                            else -> null
                                        }
                                        ) as? T
                                }
                                baResult
                            }
                            is String -> read(valueToConvert, type, deflt, notNull)
                            is TextNode -> read(valueToConvert.textValue(), type, deflt, notNull)
                            else -> mapper.convertValue(valueToConvert, type)
                        }
                    }
                }
            }

            @Suppress("UNCHECKED_CAST")
            val resultAsT = result as? T
            if (notNull && resultAsT == null) {
                throw JsonMapperException("Result is null", value, type)
            }
            return resultAsT ?: deflt
        } catch (e: Exception) {
            if (notNull) {
                if (e is JsonMapperException) {
                    throw e
                } else {
                    throw JsonMapperException(value, type, e)
                }
            } else {
                log.error(e) { "Conversion error. Type: '$type' Value: '$valueToConvert'" }
            }
        }
        return deflt
    }

    fun <T : Any> readNotNull(json: ByteArray, type: JavaType): T {
        return readNotNull(ByteArrayInputStream(json), type)
    }

    fun <T : Any> readNotNull(inputStream: InputStream, type: KClass<T>): T {
        return readNotNull(inputStream, mapper.constructType(type.java))
    }

    fun <T : Any> readNotNull(inputStream: InputStream, type: JavaType): T {
        return try {
            mapper.readValue(inputStream, type)
        } catch (e: Exception) {
            throw JsonMapperException(null, mapper.constructType(type), e)
        }
    }

    private fun <T : Any> read(value: String?, type: JavaType, deflt: T?, notNull: Boolean): T? {

        if (value.isNullOrBlank() || value == "null") {
            if (notNull) {
                throw JsonMapperException("Value is blank", value, type)
            }
            return deflt
        }
        val valueToRead = if (value[0] == '\uFEFF') {
            value.substring(1)
        } else {
            value
        }

        val result: Any?
        if (type.rawClass == Boolean::class.java) {
            result = when (valueToRead) {
                "true" -> true
                "false" -> false
                else -> null
            }
        } else {

            val firstNotEmptyCharIdx = StringUtils.getNextNotEmptyCharIdx(valueToRead, 0)
            var firstNotEmptyChar: Char? = null
            var secondNotEmptyChar: Char? = null
            var lastNotEmptyChar: Char? = null

            if (firstNotEmptyCharIdx >= 0) {
                firstNotEmptyChar = valueToRead[firstNotEmptyCharIdx]
                val lastNotEmptyCharIdx = StringUtils.getLastNotEmptyCharIdx(valueToRead)
                if (lastNotEmptyCharIdx != -1) {
                    lastNotEmptyChar = valueToRead[lastNotEmptyCharIdx]
                }
                val secondNotEmptyCharIdx =
                    StringUtils.getNextNotEmptyCharIdx(valueToRead, firstNotEmptyCharIdx + 1)
                if (secondNotEmptyCharIdx > 0) {
                    secondNotEmptyChar = valueToRead[secondNotEmptyCharIdx]
                }
            }

            result = try {

                var res: Any? = null
                var resolved = false

                if ((
                        firstNotEmptyChar == '{' &&
                            (secondNotEmptyChar == '"' || secondNotEmptyChar == '}') && lastNotEmptyChar == '}'
                        ) ||
                    (firstNotEmptyChar == '[' && lastNotEmptyChar == ']') ||
                    (firstNotEmptyChar == '"' && lastNotEmptyChar == '"')
                ) {

                    res = try {
                        val parsed: Any? = mapper.readValue(valueToRead, type)
                        resolved = true
                        parsed
                    } catch (e: JsonParseException) {
                        if (notNull) {
                            throw JsonMapperException(valueToRead, type, e)
                        }
                        log.debug { "String is not a valid json:\n$valueToRead" }
                        resolved = false
                        null
                    }
                }

                if (resolved) {
                    res
                } else {
                    mapper.readValue(TextNode.valueOf(valueToRead).toString(), type)
                }
            } catch (e: Exception) {
                if (notNull) {
                    throw JsonMapperException(valueToRead, type, e)
                }
                log.error(e) { "Conversion error. Type: '$type' Value: '$valueToRead'" }
                null
            }
        }
        @Suppress("UNCHECKED_CAST")
        val resultAsT = result as? T?
        if (notNull && resultAsT == null) {
            throw JsonMapperException("Result is null", valueToRead, type)
        }
        return resultAsT ?: deflt
    }

    private fun isNull(value: Any?): Boolean {
        return value == null ||
            value is JsonNode && (value.isNull || value.isMissingNode) ||
            value is DataValue && value.isNull()
    }
}
