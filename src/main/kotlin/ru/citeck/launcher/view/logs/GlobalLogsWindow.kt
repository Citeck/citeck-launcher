package ru.citeck.launcher.view.logs

import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.*
import ru.citeck.launcher.view.window.AdditionalWindow
import ru.citeck.launcher.view.window.AdditionalWindowState
import java.awt.GraphicsEnvironment

object GlobalLogsWindow {

    private lateinit var statesList: SnapshotStateList<AdditionalWindowState>
    private lateinit var showDialog: (InternalParams, MutableState<Boolean>) -> (() -> Unit)

    fun show(params: LogsDialogParams) {
        val dispMode = GraphicsEnvironment.getLocalGraphicsEnvironment().defaultScreenDevice.displayMode

        var prevStateParams: InternalParams? = null
        for (idx in statesList.lastIndex downTo 0) {
            val state = statesList[idx]
            if (state.params is InternalParams && state.visible.value) {
                prevStateParams = state.params
                break
            }
        }

        val windowState = if (prevStateParams == null) {
            WindowState(
                width = (dispMode.width * 0.90).dp,
                height = (dispMode.height * 0.7).dp,
                position = WindowPosition(Alignment.Center)
            )
        } else {
            val size = prevStateParams.windowState.size
            val position = prevStateParams.windowState.position
            WindowState(
                width = size.width,
                height = size.height,
                position = WindowPosition(position.x, position.y + 30.dp)
            )
        }

        val internalParams = InternalParams(params, windowState)
        showDialog(internalParams, internalParams.visible)
    }

    @Composable
    fun LogsDialog(statesList: SnapshotStateList<AdditionalWindowState>, icon: Painter) {
        this.statesList = statesList
        showDialog = AdditionalWindow(statesList) { params: InternalParams, closeDialog ->
            Window(
                onCloseRequest = {
                    params.watcher.getOrNull()?.close()
                    params.visible.value = false
                    closeDialog()
                },
                visible = params.visible.value,
                title = params.title,
                state = params.windowState,
                icon = icon
            ) {
                LogsViewer(params.logsState)
            }
        }
    }

    private class InternalParams(
        val params: LogsDialogParams,
        val windowState: WindowState,
        val logsState: LogsState = LogsState(limit = params.limit),
        val watcher: Result<AutoCloseable> = params.listenMessages { logsState.addMsg(it) },
        val visible: MutableState<Boolean> = mutableStateOf(true),
        val title: String = params.appName.let { if (it.isBlank()) "Logs" else "Logs of $it" }
    )
}

class LogsDialogParams(
    val appName: String = "",
    val limit: Int,
    val listenMessages: ((String) -> Unit) -> Result<AutoCloseable>
)

