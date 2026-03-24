package ru.citeck.launcher.api.dto

data class ExecRequestDto(
    val command: List<String> = emptyList()
)
