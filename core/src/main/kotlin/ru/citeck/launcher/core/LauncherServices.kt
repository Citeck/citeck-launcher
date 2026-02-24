package ru.citeck.launcher.core

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
import ru.citeck.launcher.core.namespace.runtime.docker.exception.DockerNotAvailableException
import ru.citeck.launcher.core.secrets.auth.AuthSecretsService
import ru.citeck.launcher.core.secrets.storage.SecretsStorage
import ru.citeck.launcher.core.ui.HeadlessUiProvider
import ru.citeck.launcher.core.ui.UiProvider
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.workspace.WorkspaceDto
import ru.citeck.launcher.core.workspace.WorkspacesService
import java.time.Duration
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.locks.ReentrantLock

class LauncherServices(
    val uiProvider: UiProvider = HeadlessUiProvider(),
    val enableCloudConfig: Boolean = true
) {

    companion object {
        private val log = KotlinLogging.logger {}
    }

    val secretsStorage: SecretsStorage by lazy { SecretsStorage(uiProvider) }
    val authSecretsService: AuthSecretsService by lazy { AuthSecretsService() }
    val gitRepoService: GitRepoService by lazy { GitRepoService(uiProvider) }
    val entitiesService: EntitiesService by lazy {
        EntitiesService(WorkspaceDto.GLOBAL_WS_ID, this, null, uiProvider)
    }
    val workspacesService: WorkspacesService by lazy { WorkspacesService() }
    val launcherStateService: LauncherStateService by lazy { LauncherStateService() }

    val cloudConfigServer = CloudConfigServer()

    val database: Database by lazy { Database() }

    lateinit var dockerApi: DockerApi
    lateinit var actionsService: ActionsService

    private var dockerHttpClient: DockerHttpClient? = null

    private val workspaceServices = MutProp<WorkspaceServices?>(null)
    private val workspaceInitialized = AtomicBoolean(false)

    private val thisLock = ReentrantLock()

    @Volatile
    private var baseInitDone = false

    suspend fun init() {
        initBase()
        initDocker()
    }

    private fun initBase() {
        if (baseInitDone) {
            return
        }

        database.init()
        launcherStateService.init(this)

        secretsStorage.init(database)
        entitiesService.init(this)
        authSecretsService.init(this)
        gitRepoService.init(this)
        workspacesService.init(this)

        entitiesService.register(authSecretsService.getSecretEntityDef())

        if (enableCloudConfig) {
            try {
                cloudConfigServer.init()
            } catch (e: Throwable) {
                log.error(e) { "Cloud config server can't be started. External apps won't work" }
            }
        }

        Runtime.getRuntime().addShutdownHook(
            Thread {
                workspaceServices.getValue()?.dispose()
                cloudConfigServer.dispose()
            }
        )

        baseInitDone = true
    }

    fun initDocker() {
        dockerHttpClient?.close()

        val dockerClientConfig = DefaultDockerClientConfig.createDefaultConfigBuilder().build()

        val httpClient: DockerHttpClient = ApacheDockerHttpClient.Builder()
            .dockerHost(dockerClientConfig.dockerHost)
            .sslConfig(dockerClientConfig.sslConfig)
            .maxConnections(200)
            .connectionTimeout(Duration.ofMinutes(2))
            .responseTimeout(Duration.ofMinutes(10))
            .build()
        dockerHttpClient = httpClient

        val dockerClient = DockerClientImpl.getInstance(dockerClientConfig, httpClient)
        this.dockerApi = DockerApi(dockerClient, httpClient)

        try {
            dockerClient.pingCmd().exec()
        } catch (e: Exception) {
            throw DockerNotAvailableException(e)
        }

        actionsService = ActionsService()
        actionsService.register(AppImagePullAction(dockerApi, authSecretsService))
        actionsService.register(AppStartAction(dockerApi))
        actionsService.register(AppStopAction(dockerApi))
    }

    fun getWorkspaceServices(timeout: Duration? = null): MutProp<WorkspaceServices?> {
        return doWithThisLock(timeout) {
            val workspaceServices = this.workspaceServices.getValue() ?: error("WorkspaceServices is not selected")
            if (workspaceInitialized.compareAndSet(false, true)) {
                workspaceServices.init()
            }
            this.workspaceServices
        }
    }

    fun setWorkspace(workspace: String) = doWithThisLock {

        val workspaceDto = entitiesService.getById(WorkspaceDto::class, workspace)?.entity
            ?: error("Workspace is not found by id '$workspace'")
        val workspaceConfig = workspacesService.getWorkspaceConfig(workspaceDto)
        val workspaceServices = WorkspaceServices(this, workspaceDto, workspaceConfig, uiProvider)

        this.workspaceServices.getValue()?.dispose()
        this.workspaceServices.setValue(workspaceServices)

        workspaceInitialized.set(false)

        launcherStateService.setSelectedWorkspace(workspace)
    }

    private inline fun <T> doWithThisLock(timeout: Duration? = null, action: () -> T): T {
        if (timeout != null) {
            if (!thisLock.tryLock(timeout.toMillis(), TimeUnit.MILLISECONDS)) {
                throw TimeoutException("${this::class.simpleName} lock timed out. Timeout: $timeout")
            }
        } else {
            thisLock.lock()
        }
        try {
            return action.invoke()
        } finally {
            thisLock.unlock()
        }
    }
}
