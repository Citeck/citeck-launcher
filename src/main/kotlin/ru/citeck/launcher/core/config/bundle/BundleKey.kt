package ru.citeck.launcher.core.config.bundle

import kotlin.math.min

class BundleKey(val rawKey: String) : Comparable<BundleKey> {

    private val versionParts: List<Int>
    private var suffix = ""

    init {
        val versionParts = ArrayList<Int>()
        rawKey.split(".").forEach { keyPart ->
            val intValue = keyPart.toIntOrNull()
            if (intValue != null) {
                versionParts.add(intValue)
            } else {
                var suffix = keyPart
                val numPart = suffix.substringBefore('-', "").toIntOrNull()
                if (numPart != null) {
                    versionParts.add(numPart)
                    suffix = suffix.substringAfter("-")
                }
                this.suffix = suffix
            }
        }
        var nonZeroNumsCount = versionParts.size
        while (nonZeroNumsCount > 0 && versionParts[nonZeroNumsCount - 1] == 0) {
            nonZeroNumsCount--
        }
        this.versionParts = versionParts.subList(0, nonZeroNumsCount)
    }

    override fun compareTo(other: BundleKey): Int {
        val commonSize = min(versionParts.size, other.versionParts.size)
        for (idx in 0 until commonSize) {
            val currentNum = versionParts[idx]
            val otherNum = other.versionParts[idx]
            if (currentNum > otherNum) {
                return 1
            } else if (currentNum < otherNum) {
                return -1
            }
        }
        return if (versionParts.size > other.versionParts.size) {
            1
        } else if (versionParts.size < other.versionParts.size) {
            -1
        } else if (suffix.isBlank() && other.suffix.isNotBlank()) {
            1
        } else if (suffix.isNotBlank() && other.suffix.isBlank()) {
            -1
        } else {
            0
        }
    }

    override fun toString(): String {
        return rawKey
    }

    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (javaClass != other?.javaClass) return false

        other as BundleKey

        return rawKey == other.rawKey
    }

    override fun hashCode(): Int {
        return rawKey.hashCode()
    }
}
