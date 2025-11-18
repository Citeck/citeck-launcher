package ru.citeck.launcher.core

import androidx.compose.runtime.Stable
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.config.bundle.BundlesService
import ru.citeck.launcher.core.config.cloud.CloudConfigServer
import ru.citeck.launcher.core.database.DataRepo
import ru.citeck.launcher.core.database.Database
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.entity.EntityDef
import ru.citeck.launcher.core.entity.EntityIdType
import ru.citeck.launcher.core.git.GitRepoService
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.license.LicenseService
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.namespace.NamespacesService
import ru.citeck.launcher.core.namespace.runtime.NamespaceRuntime
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.namespace.volume.VolumeInfo
import ru.citeck.launcher.core.namespace.volume.VolumesRepo
import ru.citeck.launcher.core.snapshot.WorkspaceSnapshots
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceDto
import ru.citeck.launcher.view.popup.CiteckWindow

@Stable
class WorkspaceServices(
    private val launcherServices: LauncherServices,
    val workspace: WorkspaceDto,
    workspaceConfig: WorkspaceConfig
) {

    companion object {
        private const val SELECTED_NS_PROP = "selectedNamespace"

        private val log = KotlinLogging.logger {}
    }

    val namespacesService: NamespacesService by lazy { NamespacesService() }
    val bundlesService: BundlesService by lazy { BundlesService() }

    val licenseService: LicenseService by lazy { LicenseService() }

    val entitiesService: EntitiesService by lazy {
        EntitiesService(workspace.id, launcherServices, this)
    }

    val gitRepoService: GitRepoService get() = launcherServices.gitRepoService
    val dockerApi: DockerApi get() = launcherServices.dockerApi
    val actionsService: ActionsService get() = launcherServices.actionsService
    val database: Database get() = launcherServices.database
    val cloudConfigServer: CloudConfigServer get() = launcherServices.cloudConfigServer
    val snapshotsService: WorkspaceSnapshots by lazy { WorkspaceSnapshots() }

    private lateinit var workspaceStateRepo: DataRepo
    val selectedNamespace = MutProp<NamespaceConfig?>("selected-namespace", null)

    val workspaceConfig = MutProp(workspaceConfig)

    fun init() {

        entitiesService.init(launcherServices)
        entitiesService.register(getVolumeEntityDef())
        snapshotsService.init(this)

        bundlesService.init(this)
        namespacesService.init(this)
        licenseService.init(launcherServices)

        workspaceStateRepo = getWorkspaceStateRepo()

        setSelectedNamespace(workspaceStateRepo[SELECTED_NS_PROP].asText())
    }

    private fun getWorkspaceStateRepo(): DataRepo {

        val repoScope = "workspace-state"
        var repoKey = workspace.id

        if (!database.hasRepo(repoScope, repoKey) && repoKey == "default") {
            val legacyKey = "DEFAULT"
            if (database.hasRepo(repoScope, legacyKey)) {
                repoKey = legacyKey
            }
        }
        return database.getDataRepo(repoScope, repoKey)
    }

    fun updateConfig(updatePolicy: GitUpdatePolicy) {
        workspaceConfig.setValue(launcherServices.workspacesService.getWorkspaceConfig(workspace, updatePolicy))
    }

    private fun getVolumeEntityDef(): EntityDef<String, VolumeInfo> {
        return EntityDef(
            EntityIdType.String,
            VolumeInfo::class,
            "Volume",
            "volume",
            { it.name },
            { it.name },
            { info, prop ->
                when (prop) {
                    "sizeMb" -> DataValue.createStr(info.sizeMb)
                    else -> DataValue.NULL
                }
            },
            createForm = null,
            editForm = null,
            emptyList(),
            emptyList(),
            customRepo = VolumesRepo(this),
            versionable = false
        )
    }

    fun setSelectedNamespace(namespaceId: String) {
        workspaceStateRepo[SELECTED_NS_PROP] = namespaceId
        val newValue = if (namespaceId.isBlank()) {
            null
        } else {
            val namespaceConfig = entitiesService.getById(NamespaceConfig::class, namespaceId)?.entity
            if (namespaceConfig == null) {
                log.error { "Namespace doesn't found by id '$namespaceId'" }
                entitiesService.getFirst(NamespaceConfig::class)?.entity
            } else {
                namespaceConfig
            }
        }
        if (selectedNamespace.getValue()?.id != newValue?.id) {
            CiteckWindow.closeAll()
        }
        selectedNamespace.setValue(newValue)
    }

    fun selectAnyExistingNamespace() {
        val currentNsDto = selectedNamespace.getValue()
        if (currentNsDto != null && entitiesService.getById(NamespaceConfig::class, currentNsDto.id) != null) {
            return
        }
        val nextNs = entitiesService.getFirst(NamespaceConfig::class)?.entity
        selectedNamespace.setValue(nextNs)
        workspaceStateRepo[SELECTED_NS_PROP] = nextNs?.id ?: ""
    }

    fun getCurrentNsRuntime(): NamespaceRuntime? {
        val currentNs = selectedNamespace.getValue() ?: return null
        return namespacesService.getRuntime(currentNs.id)
    }

    fun dispose() {
        namespacesService.dispose()
    }
}
