package ru.citeck.launcher.core.namespace.runtime.actions

import ru.citeck.launcher.core.actions.ActionContext
import ru.citeck.launcher.core.actions.ActionExecutor
import ru.citeck.launcher.core.actions.ActionParams
import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.namespace.runtime.AppRuntime
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.utils.promise.Promise

class AppStopAction(
    private val dockerApi: DockerApi
) : ActionExecutor<AppStopAction.Params, Unit> {

    companion object {
        fun execute(service: ActionsService, appRuntime: AppRuntime): Promise<Unit> {
            return service.execute(Params(appRuntime))
        }
    }

    override fun execute(context: ActionContext<Params>) {

        val runtime = context.params.appRuntime
        val appDef = runtime.def.value

        val containers = dockerApi.getContainers(runtime.nsRuntime.namespaceRef, appDef.name)

        containers.forEach { dockerApi.stopAndRemoveContainer(it) }
    }

    override fun getName(context: ActionContext<Params>): String {
        return "stop(${context.params.appRuntime.name})"
    }

    class Params(
        val appRuntime: AppRuntime
    ) : ActionParams<Unit>
}
