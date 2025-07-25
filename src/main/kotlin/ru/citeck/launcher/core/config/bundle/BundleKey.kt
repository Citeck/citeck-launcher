package ru.citeck.launcher.core.config.bundle

import ru.citeck.launcher.core.utils.StringUtils
import kotlin.math.min

data class BundleKey(val rawKey: String) : Comparable<BundleKey> {

    private val versionParts: List<Int>
    private var suffixParts = listOf<Comparable<Comparable<*>>>()

    init {

        val firstNonVersionIdx = rawKey.indexOfFirst { !it.isDigit() && it != '.' }
        val versionRawParts = if (firstNonVersionIdx == -1) {
            rawKey.split(".")
        } else {
            rawKey.substring(0, firstNonVersionIdx).split(".")
        }
        val versionParts = ArrayList<Int>()
        versionRawParts.mapNotNullTo(versionParts) { it.toIntOrNull() }

        var nonZeroNumsCount = versionParts.size
        while (nonZeroNumsCount > 0 && versionParts[nonZeroNumsCount - 1] == 0) {
            nonZeroNumsCount--
        }
        this.versionParts = versionParts.subList(0, nonZeroNumsCount)

        if (firstNonVersionIdx != -1) {
            var suffixStartIdx = firstNonVersionIdx
            if (!rawKey[suffixStartIdx].isLetterOrDigit()) {
                suffixStartIdx++
            }
            if (suffixStartIdx < rawKey.length) {
                @Suppress("UNCHECKED_CAST")
                this.suffixParts = parseSuffixParts(rawKey.substring(suffixStartIdx)) as List<Comparable<Comparable<*>>>
            }
        }
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
        var compareRes = compareElements(versionParts, other.versionParts, false)
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
            1
        } else if (first.size < second.size) {
            -1
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
