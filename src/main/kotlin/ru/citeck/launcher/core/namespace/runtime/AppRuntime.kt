package ru.citeck.launcher.core.namespace.runtime

import com.github.dockerjava.api.model.Container
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.appdef.ApplicationKind
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises

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
            if (!field.isDone()) {
                field.cancel(true)
            }
            field = newValue
        }

    val def = MutProp(def)

    val name: String get() = def.value.name
    val image: String get() = def.value.image
    val editedDef = MutProp(false)

    @Volatile
    var containers: List<Container> = emptyList()

    var pullImageIfPresent = false
    var manualStop = false

    init {
        this.def.watch { before, after ->
            if (!status.value.isStoppingState()) {
                if (before.image != after.image) {
                    status.value = AppRuntimeStatus.READY_TO_PULL
                } else {
                    status.value = AppRuntimeStatus.READY_TO_START
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
        if (status.value == AppRuntimeStatus.PULLING && !activeActionPromise.isDone()) {
            return
        }
        pullImageIfPresent = if (def.value.kind == ApplicationKind.THIRD_PARTY) {
            false
        } else {
            def.value.image.contains("snapshot", true)
        }
        manualStop = false
        status.value = AppRuntimeStatus.READY_TO_PULL
    }

    fun stop(manual: Boolean = false) {
        if (manual) {
            manualStop = true
        }
        if (!status.value.isStoppingState()) {
            status.value = AppRuntimeStatus.READY_TO_STOP
        }
    }

    fun watchLogs(tail: Int, logsCallback: (String) -> Unit): AutoCloseable {
        return dockerApi.getContainers(nsRuntime.namespaceRef, name).firstOrNull()?.let {
            dockerApi.watchLogs(it.id, tail, logsCallback)
        } ?: return AutoCloseable { }
    }
}
