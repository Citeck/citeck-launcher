package ru.citeck.launcher.core.secrets.auth

enum class AuthType(val displayName: String) {
    NONE("None"),
    TOKEN("Token"),
    BASIC("Basic (Username/Password)");

    companion object {
        const val BASIC_NAME = "BASIC"
        const val TOKEN_NAME = "TOKEN"
        const val NONE_NAME = "NONE"
    }
}
