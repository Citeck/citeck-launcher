package ru.citeck.launcher.api.dto

data class NamespaceDto(
    val id: String = "",
    val name: String = "",
    val status: String = "",
    val bundleRef: String = "",
    val apps: List<AppDto> = emptyList()
)
