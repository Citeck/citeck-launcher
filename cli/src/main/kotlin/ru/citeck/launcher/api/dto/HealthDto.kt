package ru.citeck.launcher.api.dto

data class HealthDto(
    val healthy: Boolean = false,
    val checks: List<HealthCheckDto> = emptyList()
)

data class HealthCheckDto(
    val name: String = "",
    val status: String = "",
    val message: String = ""
)
