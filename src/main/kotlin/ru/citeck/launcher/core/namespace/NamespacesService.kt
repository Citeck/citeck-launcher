package ru.citeck.launcher.core.namespace

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.config.bundle.BundleRef
import ru.citeck.launcher.core.entity.EntityDef
import ru.citeck.launcher.core.entity.EntityIdType
import ru.citeck.launcher.core.namespace.NamespaceEntityDef.FORM_FIELD_BUNDLES_REPO
import ru.citeck.launcher.core.namespace.NamespaceEntityDef.FORM_FIELD_BUNDLE_KEY
import ru.citeck.launcher.core.namespace.NamespaceEntityDef.formSpec
import ru.citeck.launcher.core.namespace.gen.NamespaceGenerator
import ru.citeck.launcher.core.namespace.runtime.NamespaceRuntime
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.data.DataValue
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
        services.entitiesService.register(createEntityDef())

        fun registerNsRuntime(nsDto: NamespaceDto): NamespaceRuntime {
            val namespaceRef = NamespaceRef(services.workspace.id, nsDto.id)
            val runtime = NamespaceRuntime(
                namespaceRef,
                nsDto,
                services.workspaceConfig,
                nsAppsGenerator,
                services.actionsService,
                services.dockerApi,
                services.database.getDataRepo(NS_RT_STATE_REPO_SCOPE, getRepoKey(namespaceRef)),
                services.cloudConfigServer
            )
            namespaceRuntimes[nsDto.id] = runtime
            return runtime
        }

        services.entitiesService.getAll(NamespaceDto::class).forEach {
            registerNsRuntime(it.entity)
        }
        services.entitiesService.events.addEntityCreatedListener(NamespaceDto::class) { event ->
            val nsTemplateId = event.entity.template
            val nsTemplate = if (nsTemplateId.isEmpty()) {
                null
            } else {
                services.workspaceConfig.value.namespaceTemplates.find {
                    it.id == nsTemplateId
                } ?: error("Unknown namespace template '$nsTemplateId'")
            }
            if (event.entity.snapshot.isNotEmpty()) {
                val actionStatus = ActionStatus.getCurrentStatus()
                val namespaceRef = NamespaceRef(services.workspace.id, event.entity.id)
                val snapshotFile = services.snapshotsService.getSnapshot(
                    event.entity.snapshot,
                    actionStatus.subStatus(0.8f)
                ).get()
                services.dockerApi.importSnapshot(
                    namespaceRef,
                    snapshotFile,
                    actionStatus.subStatus(0.2f)
                )
            }
            val runtime = registerNsRuntime(event.entity)
            if (nsTemplate != null) {
                runtime.setDetachedApps(nsTemplate.detachedApps)
            }
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

    private fun createEntityDef(): EntityDef<String, NamespaceDto> {
        return EntityDef(
            idType = EntityIdType.String,
            valueType = NamespaceDto::class,
            typeId = NamespaceEntityDef.TYPE_ID,
            typeName = "Namespace",
            getId = { it.id },
            getName = { it.name },
            createForm = formSpec,
            editForm = null,
            defaultEntities = emptyList(),
            actions = emptyList(),
            toFormData = { ns ->
                val dto = if (ns == null) {
                    val wsConfig = services.workspaceConfig.value
                    val template = wsConfig.defaultNsTemplate.config.copy().withTemplate(wsConfig.defaultNsTemplate.id)

                    var bundleRef = template.bundleRef.ifEmpty {
                        BundleRef.create(wsConfig.bundleRepos.first().id, "LATEST")
                    }
                    if (bundleRef.key == "LATEST") {
                        bundleRef = services.bundlesService.getLatestRepoBundle(bundleRef.repo)
                    }
                    template.withBundleRef(bundleRef).build()
                } else {
                    ns
                }
                val data = DataValue.of(dto)
                data[FORM_FIELD_BUNDLES_REPO] = dto.bundleRef.repo
                data[FORM_FIELD_BUNDLE_KEY] = dto.bundleRef.key
                data
            },
            fromFormData = {
                val bundleRef = BundleRef.create(
                    it[FORM_FIELD_BUNDLES_REPO].asText(),
                    it[FORM_FIELD_BUNDLE_KEY].asText()
                )
                it["bundleRef"] = bundleRef
                it.getAsNotNull(NamespaceDto::class)
            }
        )
    }

    fun getRuntime(id: String): NamespaceRuntime {
        return namespaceRuntimes[id] ?: error("Runtime is not found: $id")
    }

    override fun dispose() {
        namespaceRuntimes.values.forEach { it.dispose() }
    }
}
