package ru.citeck.launcher.api.dto

data class AppDto(
    val name: String = "",
    val status: String = "",
    val image: String = "",
    val detached: Boolean = false,
    val cpu: String = "",
    val memory: String = ""
)
