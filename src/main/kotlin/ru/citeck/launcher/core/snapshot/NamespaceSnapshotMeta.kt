package ru.citeck.launcher.core.snapshot

import java.time.Instant

class NamespaceSnapshotMeta(
    val volumes: List<VolumeSnapshotMeta>,
    val createdAt: Instant
)
