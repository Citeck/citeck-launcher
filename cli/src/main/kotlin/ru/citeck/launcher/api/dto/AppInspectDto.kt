package ru.citeck.launcher.api.dto

data class AppInspectDto(
    val name: String = "",
    val containerId: String = "",
    val image: String = "",
    val status: String = "",
    val state: String = "",
    val ports: List<String> = emptyList(),
    val volumes: List<String> = emptyList(),
    val env: List<String> = emptyList(),
    val labels: Map<String, String> = emptyMap(),
    val network: String = "",
    val restartCount: Int = 0,
    val startedAt: String = "",
    val uptime: Long = 0
)
