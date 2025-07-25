package ru.citeck.launcher.view.popup

import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.WindowPosition
import androidx.compose.ui.window.WindowState
import androidx.compose.ui.window.rememberWindowState
import io.github.oshai.kotlinlogging.KotlinLogging
import java.awt.Toolkit

abstract class CiteckWindow : CiteckPopup() {

    companion object {

        val log = KotlinLogging.logger {}

        var screenSize: DpSize = DpSize(400.dp, 400.dp)
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

            val density = LocalDensity.current
            screenSize = remember(density) {
                val screenSize = Toolkit.getDefaultToolkit().screenSize
                val screenWidthPx = screenSize.width
                val screenHeightPx = screenSize.height
                with(density) {
                    DpSize(
                        screenWidthPx.toDp(),
                        screenHeightPx.toDp()
                    )
                }
            }
            for (state in this.activeWindows) {
                state.render()
            }
        }

        fun showWindow(window: CiteckWindow) {
            activeWindows.add(window)
        }
    }

    val visible = mutableStateOf(true)

    @Composable
    protected inline fun window(
        state: WindowState = rememberWindowState(
            width = 1000.dp,
            height = 800.dp,
            position = WindowPosition(Alignment.Center)
        ),
        title: String = "Citeck Launcher",
        crossinline render: @Composable PopupContext.() -> Unit
    ) {
        Window(
            onCloseRequest = { closeWindow() },
            visible = visible.value,
            title = title,
            state = state,
            icon = windowIcon
        ) {
            Column {
                render(PopupContext(this))
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
