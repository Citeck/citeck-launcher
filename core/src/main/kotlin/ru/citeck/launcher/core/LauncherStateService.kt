package ru.citeck.launcher.core

import ru.citeck.launcher.core.database.DataRepo
import ru.citeck.launcher.core.workspace.WorkspaceDto

class LauncherStateService {

    companion object {
        private const val PARAM_SELECTED_WORKSPACE = "selectedWorkspace"
    }

    private lateinit var launcherState: DataRepo

    private var selectedWorkspace: String = WorkspaceDto.DEFAULT.id

    fun init(services: LauncherServices) {
        launcherState = services.database.getDataRepo("launcher", "state")
        val selectedWsRaw = launcherState[PARAM_SELECTED_WORKSPACE].asText()
        if (selectedWsRaw.isNotBlank()) {
            selectedWorkspace = selectedWsRaw
        }
    }

    fun getSelectedWorkspace(): String {
        return selectedWorkspace
    }

    internal fun setSelectedWorkspace(selectedWs: String) {
        launcherState[PARAM_SELECTED_WORKSPACE] = selectedWs
        selectedWorkspace = selectedWs
    }
}
