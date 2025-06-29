package ru.citeck.launcher.core.secrets.auth

import com.fasterxml.jackson.annotation.JsonIgnore
import com.fasterxml.jackson.annotation.JsonSubTypes
import com.fasterxml.jackson.annotation.JsonTypeInfo

@JsonTypeInfo(
    use = JsonTypeInfo.Id.NAME,
    include = JsonTypeInfo.As.PROPERTY,
    property = "type"
)
@JsonSubTypes(
    JsonSubTypes.Type(value = AuthSecret.Basic::class, name = AuthType.BASIC_NAME),
    JsonSubTypes.Type(value = AuthSecret.Token::class, name = AuthType.TOKEN_NAME)
)
sealed class AuthSecret(val id: String, val version: Long) {

    @JsonIgnore
    abstract fun isValid(): Boolean

    class Token(
        id: String,
        version: Long,
        val token: String
    ) : AuthSecret(id, version) {

        override fun isValid(): Boolean {
            return token.isNotEmpty()
        }
    }

    class Basic(
        id: String,
        version: Long,
        val username: String,
        val password: CharArray
    ) : AuthSecret(id, version) {
        companion object {
            val EMPTY = Basic("", 0L,"", charArrayOf())
        }
        override fun isValid(): Boolean {
            return true
        }
    }
}




