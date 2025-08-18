package ru.citeck.launcher.core.appdef

import com.fasterxml.jackson.annotation.JsonIgnore
import com.fasterxml.jackson.databind.annotation.JsonDeserialize
import ru.citeck.launcher.core.namespace.init.AppInitAction
import ru.citeck.launcher.core.utils.Digest
import ru.citeck.launcher.core.utils.NameValidator
import ru.citeck.launcher.core.utils.json.Json

@JsonDeserialize(builder = ApplicationDef.Builder::class)
data class ApplicationDef(
    val name: String,
    val image: String,
    val environments: Map<String, String>,
    val cmd: List<String>?,
    val ports: List<String>,
    val volumes: List<String>,
    val initActions: List<AppInitAction>,
    val dependsOn: Set<String>,
    val startupConditions: List<StartupCondition>,
    val livenessProbe: AppProbeDef?,
    val resources: AppResourcesDef?,
    val kind: ApplicationKind,
    /**
     * Shared memory size - /dev/shm
     */
    val shmSize: String,
    val initContainers: List<InitContainerDef>
) {

    companion object {
        val EMPTY = create("").build(false)

        fun create(name: String): Builder {
            return Builder(name)
        }
    }

    fun copy(): Builder {
        return Builder(this)
    }

    private val hashField: String by lazy {
        val digest = Digest.sha256()
            .update(name)
            .update(image)
            .update(environments)
            .update(cmd)
            .update(ports)
            .update(volumes)
            .update(Json.toString(initActions))
            .update(Json.toString(livenessProbe))
            .update(Json.toString(startupConditions))
            .update(Json.toString(resources))

        if (shmSize != EMPTY.shmSize) {
            digest.update(shmSize)
        }

        initContainers.forEach {
            digest.update(it.getHash())
        }

        digest.toHex()
    }

    @JsonIgnore
    fun getHash(): String {
        return hashField
    }

    class Builder(val name: String) {

        private var image: String = ""
        private var environments: MutableMap<String, String> = LinkedHashMap()
        private var cmd: List<String>? = null
        private var ports: MutableList<String> = ArrayList()
        private var volumes: MutableList<String> = ArrayList()
        private var initActions: MutableList<AppInitAction> = ArrayList()
        private var dependsOn: MutableSet<String> = LinkedHashSet()
        private var startupConditions: List<StartupCondition> = emptyList()
        private var livenessProbe: AppProbeDef? = null
        private var resources: AppResourcesDef? = null
        private var kind: ApplicationKind = ApplicationKind.THIRD_PARTY
        private var shmSize: String = "64m"
        private var initContainers: MutableList<InitContainerDef> = ArrayList()

        constructor(base: ApplicationDef) : this(base.name) {
            this.image = base.image
            this.environments = LinkedHashMap(base.environments)
            this.cmd = base.cmd
            this.ports = ArrayList(base.ports)
            this.volumes = ArrayList(base.volumes)
            this.initActions = ArrayList(base.initActions)
            this.dependsOn = LinkedHashSet(base.dependsOn)
            this.startupConditions = base.startupConditions
            this.livenessProbe = base.livenessProbe
            this.resources = base.resources
            this.kind = base.kind
            this.shmSize = base.shmSize
            withInitContainers(base.initContainers)
        }

        fun getEnv(name: String): String? {
            return environments[name]
        }

        fun withImage(image: String): Builder {
            this.image = image
            return this
        }

        fun addEnv(key: String, value: String): Builder {
            this.environments[key] = value
            return this
        }

        fun withEnvironments(environments: Map<String, String>): Builder {
            this.environments = LinkedHashMap(environments)
            return this
        }

        fun withCmd(cmd: List<String>?): Builder {
            this.cmd = cmd
            return this
        }

        fun withPorts(ports: List<String>): Builder {
            this.ports = ArrayList(ports)
            return this
        }

        fun addPort(port: String): Builder {
            this.ports.add(port)
            return this
        }

        fun withVolumes(volumes: List<String>): Builder {
            this.volumes = ArrayList(volumes)
            return this
        }

        fun addVolume(volume: String): Builder {
            this.volumes.add(volume)
            return this
        }

        fun withInitActions(initAction: List<AppInitAction>): Builder {
            this.initActions = ArrayList(initAction)
            return this
        }

        fun addInitAction(initAction: AppInitAction): Builder {
            this.initActions.add(initAction)
            return this
        }

        fun withDependsOn(dependsOn: Set<String>): Builder {
            this.dependsOn = LinkedHashSet(dependsOn)
            return this
        }

        fun addDependsOn(dependsOn: String): Builder {
            this.dependsOn.add(dependsOn)
            return this
        }

        fun withStartupCondition(startupCondition: StartupCondition?): Builder {
            return withStartupConditions(startupCondition?.let { listOf(it) })
        }

        fun withStartupConditions(startupCondition: List<StartupCondition>?): Builder {
            this.startupConditions = startupCondition ?: emptyList()
            return this
        }

        fun withLivenessProbe(livenessProbe: AppProbeDef?): Builder {
            this.livenessProbe = livenessProbe
            return this
        }

        fun withResources(resources: AppResourcesDef?): Builder {
            this.resources = resources
            return this
        }

        fun withKind(kind: ApplicationKind): Builder {
            this.kind = kind
            return this
        }

        fun withShmSize(shmSize: String): Builder {
            this.shmSize = shmSize
            return this
        }

        fun withInitContainers(initContainers: List<InitContainerDef>?): Builder {
            this.initContainers = ArrayList(initContainers ?: emptyList())
            return this
        }

        @JvmOverloads
        fun build(validate: Boolean = true): ApplicationDef {
            if (validate) {
                NameValidator.validate(name)
            }
            return ApplicationDef(
                name = name,
                image = image,
                environments = environments,
                cmd = cmd,
                ports = ports,
                volumes = volumes,
                initActions = initActions,
                dependsOn = dependsOn,
                startupConditions = startupConditions,
                livenessProbe = livenessProbe,
                resources = resources,
                kind = kind,
                shmSize = shmSize,
                initContainers = initContainers
            )
        }
    }
}
