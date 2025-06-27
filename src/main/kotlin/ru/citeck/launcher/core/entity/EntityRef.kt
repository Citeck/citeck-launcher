package ru.citeck.launcher.core.entity

import com.fasterxml.jackson.annotation.JsonCreator
import com.fasterxml.jackson.annotation.JsonValue

class EntityRef private constructor(typeId: String, localId: String) {

    companion object {
        val EMPTY = EntityRef("", "")

        @JsonCreator
        fun valueOf(value: String): EntityRef {
            if (value.isBlank()) {
                return EMPTY
            }
            val parts = value.split("@")
            if (parts.size < 2) {
                error("Invalid ref value '$value'")
            }
            return EntityRef(parts[0], parts[1])
        }

        fun create(typeId: String, value: String): EntityRef {
            return EntityRef(typeId, value)
        }
    }

    val typeId: String
    val localId: String

    init {
        this.typeId = typeId.trim()
        this.localId = localId.trim()
    }

    fun isEmpty(): Boolean {
        return typeId.isEmpty() && localId.isEmpty()
    }

    fun isNotEmpty(): Boolean {
        return !isEmpty()
    }

    @JsonValue
    override fun toString(): String {
        return "$typeId@$localId"
    }

    override fun equals(other: Any?): Boolean {
        if (other !is EntityRef) return false
        return typeId == other.typeId && localId == other.localId
    }

    override fun hashCode(): Int {
        return 31 * typeId.hashCode() + localId.hashCode()
    }
}

