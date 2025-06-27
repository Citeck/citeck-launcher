package ru.citeck.launcher.core.config.bundle

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
                value.substring(0, lastDelimIdx),
                value.substring(lastDelimIdx + 1)
            )
        }

        fun create(repo: String, key: String): BundleRef {
            if (repo.isBlank() || key.isBlank()) {
                error("Invalid ref: '$repo:$key'")
            }
            return BundleRef(repo.trim(), key.trim())
        }
    }

    fun ifEmpty(other: () -> BundleRef): BundleRef {
        return if (isEmpty()) {
            other.invoke()
        } else {
            this
        }
    }

    fun isEmpty(): Boolean {
        return repo.isEmpty() && key.isEmpty()
    }

    @JsonValue
    override fun toString(): String {
        return "$repo:$key"
    }
}
