package ru.citeck.launcher.core.namespace.gen

import ru.citeck.launcher.core.namespace.init.AppInitAction
import ru.citeck.launcher.core.namespace.init.ExecShell
import ru.citeck.launcher.core.workspace.WorkspaceConfig

object NamespaceGeneratorTestFixture {

    const val GATEWAY_PORT = "17020"
    const val AI_PORT = "8613"
    const val STT_IMAGE = "stt-sidecar:latest"
    const val AI_IMAGE = "ai:latest"
    val DEFAULT_STT_PORT = WorkspaceConfig.SttSidecarProps.DEFAULT.port

    fun shellCommands(actions: List<AppInitAction>): List<String> {
        return actions.filterIsInstance<ExecShell>().map { it.command }
    }
}
