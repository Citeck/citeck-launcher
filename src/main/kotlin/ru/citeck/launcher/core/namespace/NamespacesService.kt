package ru.citeck.launcher.core.namespace

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.namespace.gen.NamespaceGenerator
import ru.citeck.launcher.core.namespace.runtime.NamespaceRuntime
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.workspace.WorkspacesService
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.ConcurrentHashMap
import kotlin.io.path.deleteExisting
import kotlin.io.path.exists
import kotlin.io.path.isDirectory

class NamespacesService : Disposable {

    companion object {
        private val log = KotlinLogging.logger {}

        private const val NS_RT_STATE_REPO_SCOPE = "namespace-runtime-state"

        private fun getRepoKey(namespaceRef: NamespaceRef): String {
            return namespaceRef.workspace + ":" + namespaceRef.namespace
        }

        fun getNamespaceDir(namespaceRef: NamespaceRef): Path {
            return WorkspacesService.getWorkspaceDir(namespaceRef.workspace)
                .resolve("ns")
                .resolve(namespaceRef.namespace)
        }
    }

    private lateinit var services: WorkspaceServices

    private val namespaceRuntimes = ConcurrentHashMap<String, NamespaceRuntime>()

    val nsAppsGenerator = NamespaceGenerator()

    fun init(services: WorkspaceServices) {

        this.services = services
        nsAppsGenerator.init(services)

        fun registerNsRuntime(nsDto: NamespaceDto) {
            val namespaceRef = NamespaceRef(services.workspace.id, nsDto.id)
            namespaceRuntimes[nsDto.id] = NamespaceRuntime(
                namespaceRef,
                nsDto,
                services.workspaceConfig,
                nsAppsGenerator,
                services.actionsService,
                services.dockerApi,
                services.database.getDataRepo(NS_RT_STATE_REPO_SCOPE, getRepoKey(namespaceRef)),
                services.cloudConfigServer
            )
        }

        services.entitiesService.getAll(NamespaceDto::class).forEach {
            registerNsRuntime(it.entity)
        }
        services.entitiesService.events.addEntityCreatedListener(NamespaceDto::class) { event ->
            registerNsRuntime(event.entity)
            services.setSelectedNamespace(event.entity.id)
        }
        services.entitiesService.events.addEntityDeletedListener(NamespaceDto::class) { event ->
            val namespaceId = event.entity.id
            val runtime = namespaceRuntimes[namespaceId]
            if (runtime != null) {
                runtime.stop().get(Duration.ofMinutes(5))
                namespaceRuntimes.remove(namespaceId)
            }
            val namespaceRef = NamespaceRef(services.workspace.id, namespaceId)
            services.database.deleteRepo(NS_RT_STATE_REPO_SCOPE, getRepoKey(namespaceRef))
            val nsDir = getNamespaceDir(namespaceRef)
            try {
                if (nsDir.exists()) {
                    if (nsDir.isDirectory()) {
                        nsDir.toFile().deleteRecursively()
                    } else {
                        nsDir.deleteExisting()
                    }
                }
            } catch (e: Throwable) {
                log.error(e) { "Could not delete namespace directory: $nsDir" }
            }
            for (volume in services.dockerApi.getVolumes(namespaceRef)) {
                try {
                    services.dockerApi.deleteVolume(volume.name)
                } catch (e: Throwable) {
                    log.error(e) { "Volume deletion failed: '${volume.name}'" }
                }
            }
            services.selectAnyExistingNamespace()
        }
    }

    fun getRuntime(id: String): NamespaceRuntime {
        return namespaceRuntimes[id] ?: error("Runtime is not found: $id")
    }

    override fun dispose() {
        namespaceRuntimes.values.forEach { it.dispose() }
    }
}
