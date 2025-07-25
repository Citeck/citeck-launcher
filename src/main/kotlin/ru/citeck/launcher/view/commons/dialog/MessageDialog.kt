package ru.citeck.launcher.view.commons.dialog

import androidx.compose.material3.*
import androidx.compose.runtime.*
import ru.citeck.launcher.view.popup.CiteckDialog
import ru.citeck.launcher.view.popup.DialogWidth

class MessageDialog(
    private val params: GlobalMsgDialogParams
) : CiteckDialog() {

    companion object {
        fun show(text: String, title: String = "") {
            showDialog(MessageDialog(GlobalMsgDialogParams(title, text, DialogWidth.MEDIUM)))
        }

        fun show(params: GlobalMsgDialogParams) {
            showDialog(MessageDialog(params))
        }
    }

    @Composable
    override fun render() {
        dialog(params.width) {
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
