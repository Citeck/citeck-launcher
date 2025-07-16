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
            } else if (after == AppRuntimeStatus.STOPPED) {
                this.containers = emptyList()
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
}
