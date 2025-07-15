package ru.citeck.launcher.core

import androidx.compose.runtime.Stable
import com.github.dockerjava.core.DefaultDockerClientConfig
import com.github.dockerjava.core.DockerClientImpl
import com.github.dockerjava.httpclient5.ApacheDockerHttpClient
import com.github.dockerjava.transport.DockerHttpClient
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.config.cloud.CloudConfigServer
import ru.citeck.launcher.core.database.Database
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.git.GitRepoService
import ru.citeck.launcher.core.namespace.runtime.actions.AppImagePullAction
import ru.citeck.launcher.core.namespace.runtime.actions.AppStartAction
import ru.citeck.launcher.core.namespace.runtime.actions.AppStopAction
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.secrets.auth.AuthSecretsService
import ru.citeck.launcher.core.secrets.storage.SecretsStorage
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.workspace.WorkspaceDto
import ru.citeck.launcher.core.workspace.WorkspacesService
import java.time.Duration
import java.util.concurrent.atomic.AtomicBoolean

@Stable
class LauncherServices {

    companion object {
        private val log = KotlinLogging.logger {}
    }

    val secretsStorage: SecretsStorage by lazy { SecretsStorage() }
    val authSecretsService: AuthSecretsService by lazy { AuthSecretsService() }
    val gitRepoService: GitRepoService by lazy { GitRepoService() }
    val entitiesService: EntitiesService by lazy { EntitiesService(WorkspaceDto.GLOBAL_WS_ID) }
    val workspacesService: WorkspacesService by lazy { WorkspacesService() }
    val launcherStateService: LauncherStateService by lazy { LauncherStateService() }

    val cloudConfigServer = CloudConfigServer()

    val database: Database by lazy { Database() }

    lateinit var dockerApi: DockerApi
    lateinit var actionsService: ActionsService

    private val workspaceServices = MutProp<WorkspaceServices?>(null)
    private val workspaceInitialized = AtomicBoolean(false)

    suspend fun init() {
        database.init()
        secretsStorage.init(database)
        entitiesService.init(this)
        authSecretsService.init(this)
        gitRepoService.init(this)
        workspacesService.init(this)
        launcherStateService.init(this)

        entitiesService.register(authSecretsService.getSecretEntityDef())

        try {
            cloudConfigServer.init()
        } catch (e: Throwable) {
            log.error(e) { "Cloud config server can't be started. External apps won't work" }
        }

        Runtime.getRuntime().addShutdownHook(Thread {
            workspaceServices.getValue()?.dispose()
            cloudConfigServer.dispose()
        })

        val dockerClientConfig = DefaultDockerClientConfig.createDefaultConfigBuilder().build()

        val httpClient: DockerHttpClient = ApacheDockerHttpClient.Builder()
            .dockerHost(dockerClientConfig.dockerHost)
            .sslConfig(dockerClientConfig.sslConfig)
            .maxConnections(200)
            .connectionTimeout(Duration.ofMinutes(2))
            .responseTimeout(Duration.ofMinutes(10))
            .build()

        val dockerClient = DockerClientImpl.getInstance(dockerClientConfig, httpClient)
        this.dockerApi = DockerApi(dockerClient)

        actionsService = ActionsService()
        actionsService.register(AppImagePullAction(dockerApi, authSecretsService))
        actionsService.register(AppStartAction(dockerApi))
        actionsService.register(AppStopAction(dockerApi))
    }

    @Synchronized
    fun getWorkspaceServices(): MutProp<WorkspaceServices?> {
        val workspaceServices = this.workspaceServices.getValue() ?: error("WorkspaceServices is not selected")
        if (workspaceInitialized.compareAndSet(false, true)) {
            workspaceServices.init()
        }
        return this.workspaceServices
    }

    @Synchronized
    fun setWorkspace(workspace: String) {

        val workspaceDto = entitiesService.getById(WorkspaceDto::class, workspace)?.entity
            ?: error("Workspace is not found by id '$workspace'")
        val workspaceConfig = workspacesService.getWorkspaceConfig(workspaceDto)
        val workspaceServices = WorkspaceServices(this, workspaceDto, workspaceConfig)

        this.workspaceServices.getValue()?.dispose()
        this.workspaceServices.setValue(workspaceServices)

        workspaceInitialized.set(false)

        launcherStateService.setSelectedWorkspace(workspace)
    }
}
