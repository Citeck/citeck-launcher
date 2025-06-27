package ru.citeck.launcher.core.git

import ru.citeck.launcher.core.secrets.auth.AuthType
import java.nio.file.Path
import java.time.Duration

data class GitRepoProps(
    val path: Path,
    val url: String,
    val branch: String,
    val pullPeriod: Duration,
    val authId: String,
    val authType: AuthType
)
