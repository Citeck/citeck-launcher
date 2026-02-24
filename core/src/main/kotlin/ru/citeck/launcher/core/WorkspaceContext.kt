package ru.citeck.launcher.core

import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.bundle.BundlesService
import ru.citeck.launcher.core.config.cloud.CloudConfigServer
import ru.citeck.launcher.core.git.GitRepoService
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceDto
import java.nio.file.Path

interface WorkspaceContext {
    val workspace: WorkspaceDto
    val workspaceConfig: MutProp<WorkspaceConfig>
    val gitRepoService: GitRepoService
    val dockerApi: DockerApi
    val actionsService: ActionsService
    val bundlesService: BundlesService
    val cloudConfigServer: CloudConfigServer
    val bundlesDir: Path
    val workspaceRepoDir: Path
    val repoAuthId: String
}
