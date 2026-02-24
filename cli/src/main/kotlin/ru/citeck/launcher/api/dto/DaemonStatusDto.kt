package ru.citeck.launcher.api.dto

data class DaemonStatusDto(
    val running: Boolean = false,
    val pid: Long = -1,
    val uptime: Long = 0,
    val version: String = "",
    val workspace: String = "",
    val socketPath: String = ""
)
