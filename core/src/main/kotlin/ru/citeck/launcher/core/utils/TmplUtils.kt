package ru.citeck.launcher.core.utils

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.utils.data.DataValue

object TmplUtils {

    private val log = KotlinLogging.logger {}

    private const val CURLY_BRACE_OPEN = '{'
    private const val CURLY_BRACE_CLOSE = '}'

    private const val PH_START_1 = "\$$CURLY_BRACE_OPEN"
    private const val PH_END_1 = "$CURLY_BRACE_CLOSE"
    private const val PH_START_2 = "$CURLY_BRACE_OPEN$CURLY_BRACE_OPEN"
    private const val PH_END_2 = "$CURLY_BRACE_CLOSE$CURLY_BRACE_CLOSE"

    @JvmStatic
    fun getAtts(value: Any?): Set<String> {

        if (value == null) {
            return emptySet()
        }
        return getAtts(DataValue.create(value))
    }

    @JvmStatic
    @JvmOverloads
    fun getAtts(value: DataValue?, result: MutableSet<String> = HashSet()): Set<String> {

        if (value == null || value.isNull()) {
            return emptySet()
        }
        if (value.isArray()) {
            for (element in value) {
                getAtts(element, result)
            }
        } else if (value.isObject()) {
            value.forEach { _, v -> getAtts(v, result) }
        } else if (value.isTextual()) {
            getAtts(value.asText(), result)
        }
        return result
    }

    @JvmStatic
    @JvmOverloads
    fun getAtts(text: String?, result: MutableSet<String> = HashSet()): Set<String> {
        if (text == null) {
            return emptySet()
        }
        return if (text.contains(PH_START_1)) {
            getAtts(text, PH_START_1, PH_END_1, result)
        } else {
            getAtts(text, PH_START_2, PH_END_2, result)
        }
    }

    @JvmStatic
    @JvmOverloads
    fun getAtts(text: String?, start: String, end: String, result: MutableSet<String> = HashSet()): Set<String> {

        val firstPlaceholderIdx = text?.indexOf(start) ?: -1
        if (text == null || firstPlaceholderIdx == -1) {
            return emptySet()
        }

        var idx = firstPlaceholderIdx
        while (idx > -1) {

            if (isEscapedChar(text, idx)) {
                idx = text.indexOf(start, idx + 1)
                continue
            }

            val startIdx = idx + 2
            val endIdx = findPlaceholderEndIdx(startIdx, text, end)

            if (endIdx == -1) {
                break
            }
            val key = text.substring(startIdx, endIdx)
            if (key.isNotBlank()) {
                result.add(key)
            }
            idx = text.indexOf(start, endIdx + end.length)
        }

        return result
    }

    @JvmStatic
    fun <T : Any> applyAtts(value: T, attributes: DataValue): T {

        val result: DataValue = applyAtts(DataValue.create(value), attributes)

        return if (result.isNull()) {
            log.warn { "Apply atts failed for value $value and attributes $attributes" }
            value
        } else {
            result.getAsNotNull(value::class)
        }
    }

    @JvmStatic
    fun applyAtts(value: DataValue?, attributes: DataValue): DataValue {

        if (value == null || value.isNull()) {
            return DataValue.NULL
        }
        if (value.isArray()) {
            val result = DataValue.createArr()
            for (element in value) {
                result.add(applyAtts(element, attributes))
            }
            return result
        }
        if (value.isObject()) {
            val result = DataValue.createObj()
            value.forEach { k, v ->
                result[k] = applyAtts(v, attributes)
            }
            return result
        } else if (value.isTextual()) {
            return applyAtts(value.asText(), attributes)
        }
        return value.copy()
    }

    @JvmStatic
    fun applyAtts(value: String?, attributes: DataValue): DataValue {
        if (value == null) {
            return DataValue.NULL
        }
        return if (value.contains(PH_START_1)) {
            applyAtts(value, attributes, PH_START_1, PH_END_1)
        } else {
            applyAtts(value, attributes, PH_START_2, PH_END_2)
        }
    }

    @JvmStatic
    fun applyAtts(value: String?, attributes: DataValue, phStart: String, phEnd: String): DataValue {

        if (value == null) {
            return DataValue.NULL
        }
        val firstPlaceholderIdx = value.indexOf(phStart)
        if (value.isBlank() || firstPlaceholderIdx == -1) {
            return DataValue.createStr(value)
        }
        if (firstPlaceholderIdx == 0 &&
            findPlaceholderEndIdx(phStart.length, value, phEnd) == value.length - phEnd.length
        ) {

            return attributes[value.substring(phStart.length, value.length - phEnd.length)]
        }

        val sb = StringBuilder()
        var prevIdx = 0
        var idx = firstPlaceholderIdx
        while (idx >= 0) {
            if (isEscapedChar(value, idx)) {
                if (idx > prevIdx + 1) {
                    sb.append(value, prevIdx, idx - 1)
                    prevIdx = idx
                } else {
                    prevIdx++
                }
                idx = value.indexOf(phStart, idx + 1)
                continue
            }
            if (idx > prevIdx) {
                sb.append(value, prevIdx, idx)
                prevIdx = idx
            }
            idx = findPlaceholderEndIdx(idx + phStart.length, value, phEnd)
            if (idx != -1) {
                val key = value.substring(prevIdx + phStart.length, idx)
                if (key.isNotBlank()) {
                    sb.append(attributes[key].asText())
                }
                prevIdx = idx + phEnd.length
                idx = value.indexOf(phStart, idx + 1)
            }
        }
        if (prevIdx < value.length) {
            sb.append(value, prevIdx, value.length)
        }
        return DataValue.createStr(sb.toString())
    }

    private fun findPlaceholderEndIdx(fromIdx: Int, text: String, phEnd: String): Int {

        var openInnerBraces = 0
        var currentIndex = fromIdx
        var endIdx = -1

        if (phEnd.length == 1 && phEnd[0] == CURLY_BRACE_CLOSE) {

            while (currentIndex < text.length) {
                val char = text[currentIndex]

                if (char == CURLY_BRACE_OPEN) {
                    openInnerBraces++
                } else if (char == CURLY_BRACE_CLOSE) {
                    if (openInnerBraces > 0) {
                        openInnerBraces--
                    } else {
                        endIdx = currentIndex
                        break
                    }
                }
                currentIndex++
            }
        } else {

            return text.indexOf(phEnd, currentIndex)
        }

        return endIdx
    }

    private fun isEscapedChar(value: String, idx: Int): Boolean {
        var backSlashesCount = 0
        var backSlashesIdx = idx
        while (backSlashesIdx > 0 && value[backSlashesIdx - 1] == '\\') {
            backSlashesCount++
            backSlashesIdx--
        }
        return backSlashesCount % 2 == 1
    }
}
