package ru.citeck.launcher.core.workspace

import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.database.Database
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.git.GitRepoProps
import ru.citeck.launcher.core.git.GitRepoService
import ru.citeck.launcher.core.utils.json.Yaml
import java.nio.file.Path
import java.util.concurrent.ConcurrentHashMap
import kotlin.io.path.exists

class WorkspacesService {

    companion object {
        private const val WS_CONFIG_FILE = "workspace.yml"

        fun getWorkspaceDir(workspaceId: String): Path {
            return AppDir.PATH.resolve("ws").resolve(workspaceId)
        }

        fun getRepoAuthId(workspaceId: String): String {
            return "ws:$workspaceId:repo"
        }
    }

    private lateinit var database: Database
    private lateinit var entitiesService: EntitiesService
    private lateinit var gitRepoService: GitRepoService

    private val workspacesConfigs = ConcurrentHashMap<String, WorkspaceConfig>()

    fun init(services: LauncherServices) {

        gitRepoService = services.gitRepoService
        database = services.database
        entitiesService = services.entitiesService

        services.entitiesService.register(WorkspaceEntityDef.definition)
        entitiesService.register(WorkspaceEntityDef.definition)

        entitiesService.events.addEntityCreatedListener(WorkspaceDto::class) { event ->
            loadWorkspaceConfig(event.entity)
        }
        entitiesService.events.addEntityDeletedListener(WorkspaceDto::class) { event ->
            val wsRoot = getWorkspaceDir(event.entity.id).toFile()
            if (wsRoot.exists()) {
                wsRoot.deleteRecursively()
            }
        }
    }


    fun getWorkspaceConfig(workspace: WorkspaceDto): WorkspaceConfig {
        var config = workspacesConfigs[workspace.id]
        if (config == null) {
            config = loadWorkspaceConfig(workspace)
            workspacesConfigs[workspace.id] = config
            database.getTxnContext().doAfterRollback {
                workspacesConfigs.remove(workspace.id)
            }
        }
        return config
    }

    private fun loadWorkspaceConfig(workspace: WorkspaceDto): WorkspaceConfig {

        val txnContext = database.getTxnContext()

        val workspaceRepoDir = getWorkspaceDir(workspace.id).resolve("repo")
        if (!workspaceRepoDir.exists()) {
            txnContext.doAfterRollback {
                if (workspaceRepoDir.exists()) {
                    workspaceRepoDir.toFile().deleteRecursively()
                }
            }
        }

        workspaceRepoDir.toFile().mkdirs()
        val repoRoot = gitRepoService.initRepo(
            GitRepoProps(
                workspaceRepoDir,
                workspace.repoUrl,
                workspace.repoBranch,
                workspace.repoPullPeriod,
                "ws:${workspace.id}:repo",
                workspace.authType
            )
        ).root

        val wsConfigFile = repoRoot.resolve(WS_CONFIG_FILE)
        if (!wsConfigFile.exists()) {
            error("Workspace config file is not found in repo '${workspace.repoUrl}'")
        }
        return Yaml.read(wsConfigFile, WorkspaceConfig::class)
    }
}
