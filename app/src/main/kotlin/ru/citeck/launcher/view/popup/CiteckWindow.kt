package ru.citeck.launcher.view.popup

import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.WindowPosition
import androidx.compose.ui.window.WindowState
import androidx.compose.ui.window.rememberWindowState
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.MainWindowHolder
import java.awt.Dimension
import java.awt.GraphicsEnvironment
import java.awt.Toolkit

abstract class CiteckWindow : CiteckPopup(CiteckPopupKind.WINDOW) {

    companion object {

        val log = KotlinLogging.logger {}

        private val activeWindows: MutableList<CiteckWindow> = mutableStateListOf()

        lateinit var windowIcon: Painter

        fun hasActiveWindows(): Boolean {
            return this.activeWindows.isNotEmpty()
        }

        fun closeAll() {
            this.activeWindows.forEach { it.closeWindow() }
        }

        @Composable
        fun renderWindows(windowIcon: Painter) {
            this.windowIcon = windowIcon
            for (state in this.activeWindows) {
                state.render()
            }
        }

        fun showWindow(window: CiteckWindow) {
            activeWindows.add(window)
        }
    }

    val visible = mutableStateOf(true)
    val dialogs: MutableList<CiteckPopup> = mutableStateListOf()

    protected fun showDialog(dialog: CiteckDialog): CiteckDialog {
        dialogs.add(dialog)
        dialog.setContainer(dialogs)
        return dialog
    }

    protected var screenSize: DpSize = evalCurrentScreenSize()

    private fun evalCurrentScreenSize(): DpSize {
        val graphicsEnvironment = GraphicsEnvironment.getLocalGraphicsEnvironment()
        val location = MainWindowHolder.mainWindow.location
        var screenDimension: Dimension? = null
        for (device in graphicsEnvironment.screenDevices) {
            val bounds = device.defaultConfiguration.bounds
            if (bounds.contains(location)) {
                screenDimension = bounds.size
                break
            }
        }
        if (screenDimension == null) {
            screenDimension = Toolkit.getDefaultToolkit().screenSize
        }
        if (screenDimension == null) {
            screenDimension = Dimension(400, 400)
        }
        val width = screenDimension.width.coerceAtLeast(400)
        val height = screenDimension.height.coerceAtLeast(400)
        return DpSize(width.dp, height.dp)
    }

    @Composable
    protected inline fun window(
        state: WindowState = rememberWindowState(
            width = 1000.dp,
            height = 800.dp,
            position = WindowPosition(Alignment.Center)
        ),
        crossinline onClose: () -> Boolean,
        title: String = "Citeck Launcher",
        crossinline render: @Composable PopupContext.() -> Unit
    ) {
        Window(
            onCloseRequest = { if (onClose()) closeWindow() },
            visible = visible.value,
            title = title,
            state = state,
            icon = windowIcon
        ) {
            Column {
                render(PopupContext(this))
            }
            for (dialog in dialogs) {
                dialog.render()
            }
        }
    }

    fun closeWindow() {
        if (!visible.value) {
            return
        }
        beforeClose()
        visible.value = false
        var lastVisiblePopupIdx = activeWindows.lastIndex
        while (lastVisiblePopupIdx >= 0 && !activeWindows[lastVisiblePopupIdx].visible.value) {
            lastVisiblePopupIdx--
        }
        if (lastVisiblePopupIdx < 0) {
            activeWindows.clear()
        } else {
            while (lastVisiblePopupIdx < activeWindows.lastIndex) {
                activeWindows.removeLast()
            }
        }
    }
}
