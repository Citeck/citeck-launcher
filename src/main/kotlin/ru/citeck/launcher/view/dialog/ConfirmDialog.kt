package ru.citeck.launcher.view.dialog

import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.unit.em
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume

object ConfirmDialog : CiteckDialog<ConfirmDialogParams>() {

    private const val DEFAULT_TITLE = "Are you sure?"

    fun show(message: String, onConfirm: () -> Unit) {
        showDialog(ConfirmDialogParams(title = DEFAULT_TITLE, message = message, onConfirm = onConfirm))
    }
    fun show(message: String, width: DialogWidth, onConfirm: () -> Unit) {
        showDialog(ConfirmDialogParams(title = DEFAULT_TITLE, message = message, width = width, onConfirm = onConfirm))
    }

    suspend fun showSuspended(message: String): Boolean {
        return showSuspended(DEFAULT_TITLE, message)
    }

    suspend fun showSuspended(title: String, message: String): Boolean {
        return suspendCancellableCoroutine { continuation ->
            showDialog(
                ConfirmDialogParams(
                    title = title,
                    message = message,
                    onConfirm = { continuation.resume(true) },
                    onCancel = { continuation.resume(false) }
                )
            )
        }
    }

    @Composable
    override fun render(params: ConfirmDialogParams, closeDialog: () -> Unit) {
        content(width = params.width) {
            if (params.title.isNotEmpty()) {
                title(params.title)
            }
            Text(
                params.message,
                fontSize = 1.2.em,
            )
            buttonsRow {
                button("Cancel") {
                    closeDialog()
                    params.onCancel()
                }
                spacer()
                button("Confirm") {
                    closeDialog()
                    params.onConfirm()
                }
            }
        }
    }
}

class ConfirmDialogParams(
    val title: String,
    val message: String,
    val width: DialogWidth = DialogWidth.SMALL_2,
    val onConfirm: () -> Unit,
    val onCancel: () -> Unit = {}
)
