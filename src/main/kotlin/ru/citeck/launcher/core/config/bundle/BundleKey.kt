package ru.citeck.launcher.core.config.bundle

import ru.citeck.launcher.core.utils.StringUtils
import kotlin.math.min

data class BundleKey(val rawKey: String) : Comparable<BundleKey> {

    val scope: List<String>
    val versionParts: List<Int>
    val suffixParts: List<Comparable<Comparable<*>>>

    init {

        var keyToParse = rawKey

        val lastScopeDelimIdx = keyToParse.indexOfLast { it == '/' }
        if (lastScopeDelimIdx != -1) {
            scope = keyToParse.take(lastScopeDelimIdx).split("/").filter { it.isNotBlank() }
            keyToParse = keyToParse.substring(lastScopeDelimIdx + 1)
        } else {
            scope = emptyList()
        }

        val firstNonVersionIdx = keyToParse.indexOfFirst { !it.isDigit() && it != '.' }
        val versionRawParts = if (firstNonVersionIdx == -1) {
            keyToParse.split(".")
        } else {
            keyToParse.take(firstNonVersionIdx).split(".")
        }
        val versionParts = ArrayList<Int>()
        versionRawParts.mapNotNullTo(versionParts) { it.toIntOrNull() }

        var nonZeroNumsCount = versionParts.size
        while (nonZeroNumsCount > 0 && versionParts[nonZeroNumsCount - 1] == 0) {
            nonZeroNumsCount--
        }
        this.versionParts = versionParts.subList(0, nonZeroNumsCount)

        var suffixParts = emptyList<Comparable<*>>()
        if (firstNonVersionIdx != -1) {
            var suffixStartIdx = firstNonVersionIdx
            if (!keyToParse[suffixStartIdx].isLetterOrDigit()) {
                suffixStartIdx++
            }
            if (suffixStartIdx < keyToParse.length) {
                suffixParts = parseSuffixParts(keyToParse.substring(suffixStartIdx))
            }
        }
        @Suppress("UNCHECKED_CAST")
        this.suffixParts = suffixParts as List<Comparable<Comparable<*>>>
    }

    private fun parseSuffixParts(suffix: String): List<Comparable<*>> {
        if (suffix.isBlank()) {
            return emptyList()
        }
        return StringUtils.splitByGroups(suffix) {
            if (it.isDigit() || it == '.') 1 else 0
        }.map {
            if (it[0].isDigit()) {
                BundleKey(it)
            } else {
                it
            }
        }
    }

    override fun compareTo(other: BundleKey): Int {
        var compareRes = compareElements(scope, other.scope, true)
        if (compareRes == 0) {
            compareRes = compareElements(versionParts, other.versionParts, false)
        }
        if (compareRes == 0) {
            compareRes = compareElements(suffixParts, other.suffixParts, true)
        }
        if (compareRes == 0) {
            compareRes = rawKey.compareTo(other.rawKey)
        }
        return compareRes
    }

    private fun <T : Comparable<T>> compareElements(first: List<T>, second: List<T>, preferEmptyElements: Boolean): Int {
        if (first.isEmpty() && second.isEmpty()) {
            return 0
        }
        val commonSize = min(first.size, second.size)
        for (idx in 0 until commonSize) {
            val currentNum = first[idx]
            val otherNum = second[idx]
            if (currentNum > otherNum) {
                return 1
            } else if (currentNum < otherNum) {
                return -1
            }
        }
        if ((first.isEmpty() || second.isEmpty())) {
            return if (preferEmptyElements) {
                if (first.isEmpty()) 1 else -1
            } else {
                if (second.isEmpty()) -1 else 1
            }
        }
        return if (first.size > second.size) {
            if (preferEmptyElements) -1 else 1
        } else if (first.size < second.size) {
            if (preferEmptyElements) 1 else -1
        } else {
            0
        }
    }

    override fun toString(): String {
        return rawKey
    }

    override fun equals(other: Any?): Boolean {
        if (this === other) {
            return true
        }
        if (javaClass != other?.javaClass) {
            return false
        }
        other as BundleKey
        return rawKey == other.rawKey
    }

    override fun hashCode(): Int {
        return rawKey.hashCode()
    }
}
