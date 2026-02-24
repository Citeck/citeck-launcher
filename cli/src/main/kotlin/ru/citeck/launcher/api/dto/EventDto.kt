package ru.citeck.launcher.api.dto

data class EventDto(
    val type: String = "",
    val timestamp: Long = System.currentTimeMillis(),
    val namespaceId: String = "",
    val appName: String = "",
    val before: String = "",
    val after: String = ""
)
