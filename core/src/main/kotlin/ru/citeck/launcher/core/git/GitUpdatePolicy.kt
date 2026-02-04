package ru.citeck.launcher.core.git

enum class GitUpdatePolicy {
    REQUIRED,
    ALLOWED,
    ALLOWED_IF_NOT_EXISTS,
    DISABLED
}
