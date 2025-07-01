package ru.citeck.launcher.core.utils.data

import com.fasterxml.jackson.annotation.JsonCreator
import com.fasterxml.jackson.annotation.JsonValue
import com.fasterxml.jackson.core.JsonPointer
import com.fasterxml.jackson.databind.JsonNode
import com.fasterxml.jackson.databind.node.*
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.utils.json.Json
import java.math.BigDecimal
import java.math.BigInteger
import java.time.Instant
import java.util.function.BiConsumer
import kotlin.reflect.KClass

class DataValue private constructor(
    val value: JsonNode,
    private val unmodifiable: Boolean = false
) : Iterable<DataValue> {

    companion object {

        private val log = KotlinLogging.logger {}

        @JvmField
        val NULL = DataValue(NullNode.getInstance(), true)

        @JvmField
        val TRUE = DataValue(BooleanNode.TRUE, true)

        @JvmField
        val FALSE = DataValue(BooleanNode.FALSE, true)

        @JvmStatic
        @JsonCreator
        fun createAsIs(content: Any?): DataValue {
            val jsonValue = toJsonNode(content)
            return if (jsonValue.isNull || jsonValue.isMissingNode) {
                NULL
            } else {
                DataValue(jsonValue)
            }
        }

        @JvmStatic
        fun create(content: Any?): DataValue {
            val isTryToReadRequired = content is ByteArray || content is String
            val jsonValue = toJsonNode(content)
            return if (jsonValue.isNull || jsonValue.isMissingNode) {
                NULL
            } else if (isTryToReadRequired) {
                try {
                    if (jsonValue.isTextual && Json.isReadableValue(jsonValue)) {
                        DataValue(Json.readJson(jsonValue.asText()))
                    } else if (jsonValue.isBinary && Json.isReadableValue(jsonValue)) {
                        DataValue(Json.readJson(jsonValue.binaryValue()))
                    } else {
                        DataValue(jsonValue)
                    }
                } catch (e: Exception) {
                    log.trace(e) { "Exception while reading value" }
                    DataValue(jsonValue)
                }
            } else {
                DataValue(jsonValue)
            }
        }

        private fun toJsonNode(content: Any?): JsonNode = when (content) {
            is JsonNode -> {
                if (content.isNumber) {
                    when (content) {
                        is IntNode -> LongNode.valueOf(content.longValue())
                        is ShortNode -> LongNode.valueOf(content.longValue())
                        is BigIntegerNode -> LongNode.valueOf(content.longValue())
                        is FloatNode -> DoubleNode.valueOf(content.doubleValue())
                        is DecimalNode -> DoubleNode.valueOf(content.doubleValue())
                        else -> content
                    }
                } else {
                    content
                }
            }

            is Number -> {
                when (content) {
                    is Byte, is Short, is Int, is Long, is BigInteger -> LongNode.valueOf(content.toLong())
                    is Float, is Double, is BigDecimal -> DoubleNode.valueOf(content.toDouble())
                    else -> Json.toJson(content)
                }
            }

            is String -> TextNode.valueOf(content)
            else -> Json.toJson(content)
        }

        @JvmStatic
        fun of(content: Any?): DataValue {
            return create(content)
        }

        @JvmStatic
        fun createObj(): DataValue {
            return DataValue(Json.newObjectNode())
        }

        @JvmStatic
        fun createArr(): DataValue {
            return DataValue(Json.newArrayNode())
        }

        @JvmStatic
        fun createStr(value: Any?): DataValue {
            if (value == null) {
                return NULL
            }
            val strValue = when (value) {
                is String -> value
                is TextNode -> value.asText()
                else -> value.toString()
            }
            return DataValue(TextNode.valueOf(strValue))
        }
    }

    fun removeAll(): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        if (this.value is ContainerNode<*>) {
            this.value.removeAll()
        }
        return this
    }

    fun remove(path: String): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        (this.value as? ObjectNode)?.remove(path)
        return this
    }

    fun remove(idx: Int): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        (this.value as? ArrayNode)?.remove(idx)
        return this
    }

    fun fieldNames(): Iterator<String> {
        return value.fieldNames()
    }

    fun fieldNamesList(): List<String> {
        val res = ArrayList<String>()
        val it = fieldNames()
        while (it.hasNext()) {
            res.add(it.next())
        }
        return res
    }

    fun has(fieldName: String): Boolean {
        return value.has(fieldName)
    }

    fun has(index: Int): Boolean {
        return value.has(index)
    }

    fun copy(): DataValue {
        return createAsIs(value.deepCopy())
    }

    fun isObject() = value.isObject

    fun isValueNode() = value.isValueNode

    fun isTextual() = value.isTextual

    fun isBoolean() = value.isBoolean

    fun isNotNull() = !isNull()

    fun isNull() = value.isNull || value.isMissingNode

    fun isBinary() = value.isBinary

    fun isPojo() = value.isPojo

    fun isNumber() = value.isNumber

    fun isIntegralNumber() = value.isIntegralNumber

    fun isFloatingPointNumber() = value.isFloatingPointNumber

    fun isLong() = value.isLong

    fun isDouble() = value.isDouble

    /**
     * When working with DataValue, it is not allowed to use BigDecimal nodes.
     */
    fun isBigDecimal() = false

    /**
     * When working with DataValue, it is not allowed to use BigInteger nodes.
     */
    fun isBigInteger() = false

    fun isArray() = value.isArray

    // ====== set =======

    operator fun set(path: String, value: Any?): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        if (this.value is ObjectNode) {
            if (path.startsWith('/')) {
                var pointer = JsonPointer.valueOf(path)
                var currValue: ObjectNode = this.value
                while (pointer != pointer.last()) {
                    if (!pointer.mayMatchProperty()) {
                        error("Invalid path for set method: '$path'")
                    }
                    var nextValue = currValue.path(pointer.matchingProperty)
                    if (nextValue.isNull || nextValue.isMissingNode) {
                        nextValue = Json.newObjectNode()
                        currValue.set<ObjectNode>(pointer.matchingProperty, nextValue)
                    }
                    if (nextValue !is ObjectNode) {
                        error("Invalid value in path. Expected object or null, but received ${nextValue.nodeType}. Full path: $path")
                    }
                    currValue = nextValue
                    pointer = pointer.tail()
                }
                currValue.set<ObjectNode>(pointer.matchingProperty, toJsonNode(value))
            } else {
                this.value.set(path, toJsonNode(value))
            }
        }
        return this
    }

    operator fun set(idx: Int, value: Any?): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        val fieldValue = this.value
        if (fieldValue is ArrayNode) {
            fieldValue.set(idx, toJsonNode(value))
        }
        return this
    }

    // ====== /set =======
    // ===== set str =====

    fun setStr(path: String, value: Any?): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        return set(path, createStr(value))
    }

    fun setStr(idx: Int, value: Any?): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        return set(idx, createStr(value))
    }

    // ===== /set str =====
    // ======= get ========

    operator fun get(idx: Int): DataValue {
        return DataValue(toJsonNode(value[idx]))
    }

    fun <T : Any> get(field: String, type: KClass<T>, orElse: T?): T? {
        return Json.convert(get(field), type, orElse)
    }

    operator fun get(path: String): DataValue {
        val res = when {
            path.isNotEmpty() && path[0] == '/' -> value.at(path)
            else -> value[path]
        }
        return createAsIs(res)
    }

    fun getFirst(path: String): DataValue {
        val value = get(path)
        if (value.isArray() && value.size() > 0) {
            return value.get(0)
        }
        return NULL
    }

    // ====== /get ======
    // ===== insert =====

    fun insert(path: String, idx: Int, value: Any): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        getOrSetNewArray(path).insert(idx, value)
        return this
    }


    fun insert(idx: Int, value: Any?): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        val fieldValue = this.value
        if (fieldValue is ArrayNode) {
            fieldValue.insert(idx, toJsonNode(value))
        }
        return this
    }

    fun insertAll(path: String, idx: Int, values: Iterable<Any>): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        getOrSetNewArray(path).insertAll(idx, values)
        return this
    }

    fun insertAll(idx: Int, values: Iterable<Any>): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        val fieldValue = this.value
        if (fieldValue is ArrayNode) {
            val it = values.iterator()
            var mutIdx = idx
            while (it.hasNext()) {
                val value = it.next()
                fieldValue.insert(mutIdx++, toJsonNode(value))
            }
        }
        return this
    }

    // ===== /insert =====
    // ======= add =======

    fun add(path: String, value: Any?): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        getOrSetNewArray(path).add(value)
        return this
    }

    fun add(value: Any?): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        val fieldValue = this.value
        if (fieldValue is ArrayNode) {
            fieldValue.add(toJsonNode(value))
        }
        return this
    }

    fun addAll(path: String, values: Iterable<Any>): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        getOrSetNewArray(path).addAll(values)
        return this
    }

    fun addAll(values: Iterable<Any>): DataValue {
        if (unmodifiable) {
            throw RuntimeException("Unmodifiable")
        }
        values.forEach { add(it) }
        return this
    }

    // ======= /add =======

    private fun getOrSetNewArray(path: String): DataValue {
        var array = get(path)
        if (array.isNull()) {
            array = createAsIs(Json.newArrayNode())
            set(path, array)
        }
        return array
    }

    fun size(): Int {
        return value.size()
    }

    fun isEmpty(): Boolean {
        return if (isTextual()) {
            asText().isEmpty()
        } else {
            size() == 0
        }
    }

    fun isNotEmpty(): Boolean {
        return !isEmpty()
    }

    override fun iterator(): Iterator<DataValue> {
        return Iter(value)
    }

    fun <T> mapKV(func: (String, DataValue) -> T): List<T> {
        val result = ArrayList<T>()
        forEach { k, v -> result.add(func.invoke(k, v)) }
        return result
    }

    fun <T> map(func: (DataValue) -> T): List<T> {
        val result = ArrayList<T>()
        forEach { v -> result.add(func.invoke(v)) }
        return result
    }

    fun forEach(consumer: (String, DataValue) -> Unit) {
        val names = value.fieldNames()
        while (names.hasNext()) {
            val name = names.next()
            consumer.invoke(name, DataValue(toJsonNode(value[name])))
        }
    }

    fun forEachJ(consumer: BiConsumer<String, DataValue>) {
        forEach { k, v -> consumer.accept(k, v) }
    }

    fun canConvertToInt(): Boolean {
        return value.canConvertToInt()
    }

    fun canConvertToLong(): Boolean {
        return value.canConvertToLong()
    }

    fun textValue(): String {
        return value.textValue()
    }

    fun binaryValue(): ByteArray {
        return value.binaryValue()
    }

    fun booleanValue(): Boolean {
        return value.booleanValue()
    }

    fun numberValue(): Number {
        return value.numberValue()
    }

    fun shortValue(): Short {
        return value.shortValue()
    }

    fun intValue(): Int {
        return value.intValue()
    }

    fun longValue(): Long {
        return value.longValue()
    }

    fun floatValue(): Float {
        return value.floatValue()
    }

    fun doubleValue(): Double {
        return value.doubleValue()
    }

    fun decimalValue(): BigDecimal {
        return value.decimalValue()
    }

    fun bigIntegerValue(): BigInteger {
        return value.bigIntegerValue()
    }

    fun asText(): String {
        return if (isNull()) {
            ""
        } else {
            value.asText()
        }
    }

    fun asText(defaultValue: String?): String? {
        return value.asText(defaultValue)
    }

    fun asInt(): Int {
        return value.asInt()
    }

    fun asInt(defaultValue: Int): Int {
        return value.asInt(defaultValue)
    }

    fun asLong(): Long {
        return value.asLong()
    }

    fun asLong(defaultValue: Long): Long {
        return value.asLong(defaultValue)
    }

    fun asDouble(): Double {
        return value.asDouble()
    }

    fun asDouble(defaultValue: Double): Double {
        return value.asDouble(defaultValue)
    }

    fun asBoolean(): Boolean {
        return value.asBoolean()
    }

    fun asBoolean(defaultValue: Boolean): Boolean {
        return value.asBoolean(defaultValue)
    }

    fun getAsInstant(): Instant? {
        return getAs(Instant::class)
    }

    fun getAsInstantOrEpoch(): Instant {
        return getAs(Instant::class) ?: Instant.EPOCH
    }


    fun <T : Any> getAs(type: KClass<T>): T? {
        return Json.convert(value, type)
    }

    fun <T : Any> getAsNotNull(type: KClass<T>): T {
        return Json.convert(value, type)
    }

    fun toStrList(): List<String> {
        return toList(String::class)
    }

    /**
     * Convert internal value and return a new mutable list.
     * If internal value is not an array-like object then list with single element will be returned.
     */

    fun <T : Any> toList(elementType: KClass<T>): MutableList<T> {
        if (value.isNull) {
            return ArrayList()
        }
        val arrValue = if (value.isArray) {
            value
        } else {
            val array = Json.newArrayNode()
            array.add(value)
            array
        }
        return ArrayList(Json.convertOrNull<List<T>>(arrValue, Json.getListType(elementType)) ?: emptyList())
    }

    fun asStrList(): MutableList<String> {
        return asList(String::class)
    }

    /**
     * Convert internal value and return a new mutable list.
     * If internal value is not an array-like object then empty list will be returned.
     */

    fun <T : Any> asList(elementType: KClass<T>): MutableList<T> {
        if (value.isArray) {
            return ArrayList(Json.convert<List<T>>(value, Json.getListType(elementType)) ?: emptyList())
        }
        return ArrayList()
    }

    /**
     * Convert internal value and return a new mutable map.
     * If internal value is not a map-like object then empty list will be returned.
     */

    fun <K : Any, V : Any> asMap(keyType: KClass<K>, valueType: KClass<V>): MutableMap<K, V> {
        return LinkedHashMap(
            if (!value.isNull) {
                Json.convert<Map<K, V>>(value, Json.getMapType(keyType, valueType)) ?: emptyMap()
            } else {
                emptyMap()
            }
        )
    }

    @JsonValue
    fun asJson(): JsonNode {
        return value
    }

    override fun toString(): String {
        return value.toString()
    }

    override fun equals(other: Any?): Boolean {
        if (this === other) {
            return true
        }
        if (javaClass != other?.javaClass) {
            return false
        }

        other as DataValue

        return isJsonNodeEquals(value, other.value)
    }

    fun mergeDataFrom(value: DataValue) {

        val toValue = this.value
        val fromValue = value.value

        if (toValue !is ObjectNode || fromValue !is ObjectNode) {
            return
        }
        mergeData(toValue, fromValue)
    }

    private fun mergeData(to: ObjectNode, from: ObjectNode) {
        val fromFields = from.fieldNames()
        while (fromFields.hasNext()) {
            val name = fromFields.next()
            val fromValue = from.path(name)
            val toValue = to.get(name)
            if (fromValue !is ObjectNode || toValue !is ObjectNode) {
                to.set<ObjectNode>(name, toJsonNode(fromValue))
            } else {
                mergeData(toValue, fromValue)
            }
        }
    }

    /**
     * Compares two JSON nodes, while ignoring the type of numerical values.
     * If the absolute values of two numerical values are equal, then the type (i.e., integer or long) does not matter.
     * For comparing floating-point numbers, all numbers involved are first converted to Double.
     * In cases of integral numbers, all numbers are first converted to Long.
     * Furthermore, the method treats MISSING node as equivalent to NULL node.
     *
     * @param v0 the first JSON Node to compare.
     * @param v1 the second JSON Node to compare.
     * @return true if the two provided JSON nodes are equal
     */
    private fun isJsonNodeEquals(v0: JsonNode?, v1: JsonNode?): Boolean {
        if (v0 === v1) {
            return true
        }
        if (v0 == null || v1 == null) {
            return false
        }
        val type0 = getNodeType(v0)
        val type1 = getNodeType(v1)
        if (type0 != type1) {
            return false
        }
        when (type0) {
            JsonNodeType.NULL -> {
                return true
            }

            JsonNodeType.BOOLEAN -> {
                return v0.booleanValue() == v1.booleanValue()
            }

            JsonNodeType.BINARY -> {
                return v0.binaryValue().contentEquals(v1.binaryValue())
            }

            JsonNodeType.OBJECT -> {
                if (v0.size() != v1.size()) {
                    return false
                }
                val names = v0.fieldNames()
                while (names.hasNext()) {
                    val name = names.next()
                    if (!isJsonNodeEquals(v0.get(name), v1.get(name))) {
                        return false
                    }
                }
                return true
            }

            JsonNodeType.ARRAY -> {
                if (v0.size() != v1.size()) {
                    return false
                }
                for (idx in 0 until v0.size()) {
                    if (!isJsonNodeEquals(v0.get(idx), v1.get(idx))) {
                        return false
                    }
                }
                return true
            }

            JsonNodeType.NUMBER -> {
                if (v0.isIntegralNumber) {
                    return v1.isIntegralNumber && v0.longValue().compareTo(v1.longValue()) == 0
                } else if (v0.isFloatingPointNumber) {
                    return v1.isFloatingPointNumber && v0.doubleValue().compareTo(v1.doubleValue()) == 0
                }
            }

            else -> {}
        }
        return v0 == v1
    }

    private fun getNodeType(value: JsonNode): JsonNodeType {
        val type = value.nodeType
        if (type == JsonNodeType.MISSING) {
            return JsonNodeType.NULL
        }
        return type
    }


    override fun hashCode(): Int {
        return value.hashCode()
    }

    private fun isJsonPath(path: String): Boolean {
        return path.length >= 2 && path[0] == '$' && (path[1] == '.' || path[1] == '[')
    }


    fun asUnmodifiable(): DataValue {
        return DataValue(toJsonNode(value.deepCopy()), true)
    }


    fun isUnmodifiable(): Boolean {
        return unmodifiable
    }

    private class Iter(iterable: Iterable<JsonNode>) : Iterator<DataValue> {

        private val iterator: Iterator<JsonNode> = iterable.iterator()

        override fun hasNext(): Boolean {
            return iterator.hasNext()
        }

        override fun next(): DataValue {
            return DataValue(toJsonNode(iterator.next()))
        }
    }
}
