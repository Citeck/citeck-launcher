package ru.citeck.launcher.core.appdef

enum class ApplicationKind {
    CITECK_CORE,
    CITECK_ADDITIONAL,
    THIRD_PARTY;

    fun isCiteckApp(): Boolean {
        return this == CITECK_CORE || this == CITECK_ADDITIONAL
    }
}
