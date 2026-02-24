package ru.citeck.launcher.cli.daemon.services

import ru.citeck.launcher.cli.daemon.storage.ConfigPaths
import ru.citeck.launcher.core.WorkspaceContext
import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.bundle.BundlesService
import ru.citeck.launcher.core.config.cloud.CloudConfigServer
import ru.citeck.launcher.core.git.GitRepoService
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceDto
import java.nio.file.Path

class DaemonWorkspaceContext(
    override val workspace: WorkspaceDto,
    workspaceConfig: WorkspaceConfig,
    override val gitRepoService: GitRepoService,
    override val dockerApi: DockerApi,
    override val actionsService: ActionsService,
    override val bundlesService: BundlesService,
    override val cloudConfigServer: CloudConfigServer
) : WorkspaceContext {
    override val workspaceConfig: MutProp<WorkspaceConfig> = MutProp(workspaceConfig)
    override val bundlesDir: Path = ConfigPaths.BUNDLES_DIR
    override val workspaceRepoDir: Path = ConfigPaths.WORKSPACE_REPO_DIR
    override val repoAuthId: String = "daemon:repo"
}
