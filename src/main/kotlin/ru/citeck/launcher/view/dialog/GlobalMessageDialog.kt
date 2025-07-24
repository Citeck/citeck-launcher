package ru.citeck.launcher.view.dialog

import androidx.compose.material3.*
import androidx.compose.runtime.*

object GlobalMessageDialog : CiteckDialog<GlobalMsgDialogParams>() {

    fun show(text: String, title: String = "") {
        showDialog(GlobalMsgDialogParams(title, text, DialogWidth.MEDIUM))
    }

    fun show(params: GlobalMsgDialogParams) {
        showDialog(params)
    }

    @Composable
    override fun render(params: GlobalMsgDialogParams, closeDialog: () -> Unit) {
        content(params.width) {
            if (params.title.isNotBlank()) {
                title(params.title)
            }
            if (params.text.isNotBlank()) {
                Text(
                    text = params.text,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            buttonsRow {
                spacer()
                button("Ok") { closeDialog() }
            }
        }
    }
}

class GlobalMsgDialogParams(
    val title: String,
    val text: String,
    val width: DialogWidth = DialogWidth.MEDIUM
)
