package ru.citeck.launcher.api.dto

data class ExecResultDto(
    val exitCode: Long = -1,
    val output: String = ""
)
