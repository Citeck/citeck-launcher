package ru.citeck.launcher.core.namespace.runtime

import java.nio.file.Path

data class NsFileInfo(
    val path: Path,
    val hash: String,
    val edited: Boolean
)
