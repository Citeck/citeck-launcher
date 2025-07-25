package ru.citeck.launcher.view.popup

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.*
import io.github.oshai.kotlinlogging.KotlinLogging

abstract class CiteckDialog : CiteckPopup(CiteckPopupKind.DIALOG) {

    companion object {

        val log = KotlinLogging.logger {}

        private val activeDialogs: MutableList<CiteckPopup> = mutableStateListOf()
        lateinit var windowIcon: Painter

        fun hasActiveDialogs(): Boolean {
            return activeDialogs.isNotEmpty()
        }

        fun showDialog(dialog: CiteckDialog): CiteckDialog {
            activeDialogs.add(dialog)
            return dialog
        }

        @Composable
        fun renderDialogs(windowIcon: Painter) {
            this.windowIcon = windowIcon
            for (state in activeDialogs) {
                state.render()
            }
        }
    }

    @Composable
    protected inline fun dialog(
        width: DialogWidth = DialogWidth.MEDIUM,
        crossinline render: @Composable PopupContext.() -> Unit
    ) {
        Dialog(
            properties = DialogProperties(usePlatformDefaultWidth = false),
            onDismissRequest = {}
        ) {
            Card(
                modifier = Modifier.width(width.dp),
                shape = RoundedCornerShape(10.dp),
                colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)
            ) {
                Column(modifier = Modifier.padding(top = 15.dp, start = 20.dp, end = 20.dp)) {
                    render(PopupContext(this))
                }
            }
        }
    }

    @Composable
    protected inline fun dialogWindow(
        title: String,
        crossinline render: @Composable PopupContext.() -> Unit
    ) {
        val state = rememberDialogState(
            width = 1000.dp,
            height = 800.dp,
            position = WindowPosition(Alignment.Center)
        )
        DialogWindow(
            onCloseRequest = { closeDialog() },
            title = title,
            state = state,
            icon = windowIcon
        ) {
            Column(modifier = Modifier.padding(top = 15.dp, start = 20.dp, end = 20.dp, bottom = 15.dp)) {
                render(PopupContext(this))
            }
        }
    }

    fun closeDialog() {
        beforeClose()
        var dialogIdx = activeDialogs.size
        while (--dialogIdx >= 0) {
            if (activeDialogs[dialogIdx] === this) {
                activeDialogs.removeAt(dialogIdx)
            }
        }
    }
}
