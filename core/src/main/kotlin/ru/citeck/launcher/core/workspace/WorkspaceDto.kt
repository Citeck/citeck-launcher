package ru.citeck.launcher.core.workspace

import ru.citeck.launcher.core.secrets.auth.AuthType
import java.time.Duration

class WorkspaceDto(
    val id: String,
    val name: String,
    val repoUrl: String,
    val repoBranch: String,
    val repoPullPeriod: Duration,
    val authType: AuthType
) {

    companion object {
        val GLOBAL_WS_ID = "global"
        val DEFAULT = WorkspaceDto(
            id = "default",
            name = "Default Workspace",
            repoUrl = "https://github.com/Citeck/launcher-workspace.git",
            repoBranch = "main",
            repoPullPeriod = Duration.ofHours(6),
            authType = AuthType.NONE
        )
    }
}
