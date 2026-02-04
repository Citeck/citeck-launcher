package ru.citeck.launcher.core.snapshot

data class VolumeSnapshotMeta(
    val name: String,
    val rootStat: String = "",
    val dataFile: String
)
