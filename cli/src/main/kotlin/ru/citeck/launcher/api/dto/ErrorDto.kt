package ru.citeck.launcher.api.dto

data class ErrorDto(
    val error: String = "",
    val message: String = "",
    val details: String = ""
)
