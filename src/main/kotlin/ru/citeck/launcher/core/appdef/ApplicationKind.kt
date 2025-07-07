package ru.citeck.launcher.core.appdef

enum class ApplicationKind {
    CITECK_CORE,
    CITECK_CORE_EXTENSION,
    CITECK_ADDITIONAL,
    THIRD_PARTY;

    fun isCiteckApp(): Boolean {
        return this == CITECK_CORE || this == CITECK_CORE_EXTENSION || this == CITECK_ADDITIONAL
    }
}
