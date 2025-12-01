package ru.citeck.launcher.core.namespace.runtime

import com.github.dockerjava.api.model.Container
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.appdef.ApplicationKind
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises
import ru.citeck.launcher.core.utils.prop.MutProp
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap

class AppRuntime(
    val nsRuntime: NamespaceRuntime,
    def: ApplicationDef,
    private val dockerApi: DockerApi
) {

    val status = MutProp("${def.name}-status", AppRuntimeStatus.STOPPED)
    val statusText = MutProp("")
    @Volatile
    var activeActionPromise: Promise<*> = Promises.resolve(Unit)
        set(newValue) {
            val oldValue = field
            field = newValue
            if (!oldValue.isDone()) {
                oldValue.cancel(true)
            }
        }

    val def = MutProp(def)
    val containerStats = MutProp("${def.name}-stats", ContainerStats.EMPTY)

    val name: String get() = def.getValue().name
    val image: String get() = def.getValue().image
    val editedDef = MutProp(false)

    @Volatile
    var containers: List<Container> = emptyList()

    var pullImageIfPresent = false
    val isDetached: Boolean
        get() = nsRuntime.detachedApps.contains(name)

    val dependenciesToWait: MutableSet<String> = Collections.newSetFromMap<String>(ConcurrentHashMap())
    internal var lastDepsCheckingTime = 0L

    @Volatile
    private var statsStream: AutoCloseable? = null

    init {
        this.def.watch { before, after ->
            if (!status.getValue().isStoppingState()) {
                if (before.image != after.image) {
                    status.setValue(AppRuntimeStatus.READY_TO_PULL)
                } else {
                    status.setValue(AppRuntimeStatus.READY_TO_START)
                }
            }
        }
        status.watch { before, after ->
            if (after == AppRuntimeStatus.RUNNING) {
                this.containers = dockerApi.getContainers(nsRuntime.namespaceRef, def.name)
                startStatsStream()
            } else if (before == AppRuntimeStatus.RUNNING) {
                stopStatsStream()
            }
            if (after == AppRuntimeStatus.STOPPED) {
                this.containers = emptyList()
                containerStats.setValue(ContainerStats.EMPTY)
            }
            if (before == AppRuntimeStatus.READY_TO_PULL) {
                pullImageIfPresent = false
            }
        }
    }

    fun start() {
        if (status.getValue() == AppRuntimeStatus.PULLING && !activeActionPromise.isDone()) {
            return
        }
        val def = this.def.getValue()
        dependenciesToWait.clear()
        dependenciesToWait.addAll(def.dependsOn)
        pullImageIfPresent = if (def.kind == ApplicationKind.THIRD_PARTY) {
            false
        } else {
            def.image.contains("snapshot", true)
        }
        status.setValue(AppRuntimeStatus.READY_TO_PULL)
    }

    fun stop(manual: Boolean = false) {
        if (manual) {
            nsRuntime.addDetachedApp(name)
        }
        if (!status.getValue().isStoppingState()) {
            status.setValue(AppRuntimeStatus.READY_TO_STOP)
        }
    }

    fun watchLogs(tail: Int, logsCallback: (String) -> Unit): AutoCloseable {
        return dockerApi.getContainers(nsRuntime.namespaceRef, name).firstOrNull()?.let {
            dockerApi.watchLogs(it.id, tail, logsCallback)
        } ?: return AutoCloseable { }
    }

    private fun startStatsStream() {
        stopStatsStream()
        val container = containers.firstOrNull() ?: return
        val containerId = container.id
        statsStream = dockerApi.watchContainerStats(
            containerId = containerId,
            onStats = { stats ->
                containerStats.setValue(stats)
            },
            onError = {
                if (status.getValue() == AppRuntimeStatus.RUNNING) {
                    Thread.sleep(2000)
                    if (status.getValue() == AppRuntimeStatus.RUNNING) {
                        startStatsStream()
                    }
                }
            }
        )
    }

    private fun stopStatsStream() {
        statsStream?.close()
        statsStream = null
    }
}
