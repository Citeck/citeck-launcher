package ru.citeck.launcher.cli.daemon.services

import com.github.dockerjava.core.DefaultDockerClientConfig
import com.github.dockerjava.core.DockerClientImpl
import com.github.dockerjava.httpclient5.ApacheDockerHttpClient
import com.github.dockerjava.transport.DockerHttpClient
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.cli.daemon.storage.ConfigPaths
import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.bundle.BundlesService
import ru.citeck.launcher.core.config.cloud.CloudConfigServer
import ru.citeck.launcher.core.git.GitRepoService
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.namespace.gen.NamespaceGenerator
import ru.citeck.launcher.core.namespace.runtime.actions.AppImagePullAction
import ru.citeck.launcher.core.namespace.runtime.actions.AppStartAction
import ru.citeck.launcher.core.namespace.runtime.actions.AppStopAction
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.namespace.runtime.docker.exception.DockerNotAvailableException
import ru.citeck.launcher.core.secrets.auth.AuthSecretsService
import ru.citeck.launcher.core.ui.HeadlessUiProvider
import ru.citeck.launcher.core.ui.UiProvider
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceDto
import java.time.Duration
import kotlin.io.path.exists

class DaemonServices {

    private companion object {
        val log = KotlinLogging.logger {}
    }

    val uiProvider: UiProvider = HeadlessUiProvider()

    lateinit var dockerApi: DockerApi
        private set
    lateinit var actionsService: ActionsService
        private set

    val gitRepoService: GitRepoService by lazy { GitRepoService(uiProvider) }
    val bundlesService: BundlesService by lazy { BundlesService(uiProvider) }
    val nsAppsGenerator: NamespaceGenerator by lazy { NamespaceGenerator() }
    val cloudConfigServer: CloudConfigServer = CloudConfigServer()

    private var dockerHttpClient: DockerHttpClient? = null

    fun init() {
        ConfigPaths.ensureDirs()
        initDocker()
    }

    private fun initDocker() {
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

        val authSecrets = AuthSecretsService()

        actionsService = ActionsService()
        actionsService.register(AppImagePullAction(dockerApi, null))
        actionsService.register(AppStartAction(dockerApi))
        actionsService.register(AppStopAction(dockerApi))

        gitRepoService.init(authSecrets)
    }

    fun loadWorkspaceConfig(): WorkspaceConfig {
        try {
            return ConfigPaths.loadWorkspaceConfig(gitRepoService)
                ?: error("Workspace config not found")
        } catch (e: Throwable) {
            log.error(e) { "Failed to load workspace config" }
            throw IllegalStateException(
                "Failed to load workspace config. The workspace repository is required for the platform to operate.\n\n" +
                    "To fix this, choose one of the following options:\n\n" +
                    "  1. Ensure network connectivity and restart the daemon:\n" +
                    "       systemctl restart citeck\n\n" +
                    "  2. Manually clone the workspace repository:\n" +
                    "       git clone ${ConfigPaths.WORKSPACE_REPO_URL} ${ConfigPaths.WORKSPACE_REPO_DIR}\n\n" +
                    "  3. Extract a workspace archive into:\n" +
                    "       ${ConfigPaths.WORKSPACE_REPO_DIR}\n",
                e
            )
        }
    }

    fun reloadWorkspaceConfig(): WorkspaceConfig {
        // For reload, force git pull when possible
        val hasGitRepo = ConfigPaths.WORKSPACE_REPO_DIR.resolve(".git").exists()
        val policy = if (hasGitRepo) GitUpdatePolicy.REQUIRED else GitUpdatePolicy.ALLOWED
        return ConfigPaths.loadWorkspaceConfig(gitRepoService, policy)
            ?: error("Workspace config not found after reload")
    }

    fun createWorkspaceContext(): DaemonWorkspaceContext {
        val wsConfig = loadWorkspaceConfig()
        val workspace = WorkspaceDto(
            id = "daemon",
            name = "Daemon Workspace",
            repoUrl = "",
            repoBranch = "",
            repoPullPeriod = Duration.ofHours(6),
            authType = ru.citeck.launcher.core.secrets.auth.AuthType.NONE
        )
        val context = DaemonWorkspaceContext(
            workspace = workspace,
            workspaceConfig = wsConfig,
            gitRepoService = gitRepoService,
            dockerApi = dockerApi,
            actionsService = actionsService,
            bundlesService = bundlesService,
            cloudConfigServer = cloudConfigServer
        )
        bundlesService.init(context)
        nsAppsGenerator.init(context)
        return context
    }

    fun dispose() {
        try {
            actionsService.dispose()
        } catch (e: Throwable) {
            log.error(e) { "Error disposing actions service" }
        }
        try {
            cloudConfigServer.dispose()
        } catch (e: Throwable) {
            log.error(e) { "Error disposing cloud config server" }
        }
        try {
            dockerHttpClient?.close()
        } catch (e: Throwable) {
            log.error(e) { "Error closing Docker HTTP client" }
        }
    }
}
