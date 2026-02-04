package ru.citeck.launcher.core.utils

object MemoryUtils {

    private val MEM_REGEX = "^(\\d+(\\.\\d+)?)([a-zA-Z]{1,2})$".toRegex()
    private val UNITS: Map<String, Long> = mapOf(
        "b" to 1L,
        "k" to 1024L,
        "m" to 1024 * 1024L,
        "g" to 1024 * 1024 * 1024L
    )

    fun parseMemAmountToBytes(amount: String): Long {
        val result = MEM_REGEX.matchEntire(amount) ?: error("Memory value can't be parsed: '$amount'")
        val dimension: Long = UNITS[result.groupValues[3]] ?: error("Unknown size unit: '${result.groups[2]}'")
        return (result.groupValues[1].toDouble() * dimension).toLong()
    }
}
