package ru.citeck.launcher.view.logs

import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.window.*
import ru.citeck.launcher.view.popup.CiteckWindow

class LogsWindow(
    private val params: InternalParams
) : CiteckWindow() {

    companion object {
        fun show(params: LogsDialogParams) {
            showWindow(LogsWindow(InternalParams(params)))
        }
    }

    override fun beforeClose() {
        params.watcher.getOrNull()?.close()
    }

    @Composable
    override fun render() {
        val windowState = rememberWindowState(
            width = screenSize.width,
            height = screenSize.height * 0.9f,
            position = WindowPosition(Alignment.Center)
        )
        window(windowState, title = params.title, onClose = { true }) {
            LogsViewer(
                logsState = params.logsState,
                windowTitle = params.title,
                onClose = { closeWindow() }
            )
        }
    }

    class InternalParams(
        val params: LogsDialogParams,
        val logsState: LogsState = LogsState(limit = params.limit),
        val watcher: Result<AutoCloseable> = params.listenMessages { logsState.addMsg(it) },
        val title: String = params.appName.let { if (it.isBlank()) "Logs" else "Logs of $it" }
    )
}

class LogsDialogParams(
    val appName: String = "",
    val limit: Int,
    val listenMessages: ((String) -> Unit) -> Result<AutoCloseable>
)
