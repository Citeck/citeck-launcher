package ru.citeck.launcher.core.bundle

import com.fasterxml.jackson.annotation.JsonCreator
import com.fasterxml.jackson.annotation.JsonValue

class BundleRef private constructor(
    val repo: String,
    val key: String
) {
    companion object {
        val EMPTY = BundleRef("", "")

        @JsonCreator
        @JvmStatic
        fun valueOf(value: String): BundleRef {
            if (value.isEmpty()) {
                return EMPTY
            }
            val lastDelimIdx = value.lastIndexOf(':')
            if (lastDelimIdx == -1) {
                error("Invalid ref: '$value'")
            }
            return create(
                value.take(lastDelimIdx),
                value.substring(lastDelimIdx + 1)
            )
        }

        fun create(repo: String, key: String): BundleRef {
            if (repo.isBlank() || key.isBlank()) {
                error("Invalid ref: '$repo:$key'")
            }
            return BundleRef(repo.trim(), key.trim())
        }

        fun ifEmpty(ref: BundleRef?, other: () -> BundleRef): BundleRef {
            return if (ref == null || ref.isEmpty()) {
                other.invoke()
            } else {
                ref
            }
        }
    }

    fun ifEmpty(other: () -> BundleRef) = ifEmpty(this, other)

    fun isEmpty(): Boolean {
        return repo.isEmpty() && key.isEmpty()
    }

    @JsonValue
    override fun toString(): String {
        return "$repo:$key"
    }
}
