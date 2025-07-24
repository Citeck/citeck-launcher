package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume

object ConfirmDialog : CiteckDialog<ConfirmDialogParams>() {

    fun show(message: String, onConfirm: () -> Unit) {
        showDialog(ConfirmDialogParams(message = message, onConfirm = onConfirm))
    }

    suspend fun showSuspended(message: String): Boolean {
        return suspendCancellableCoroutine { continuation ->
            showDialog(
                ConfirmDialogParams(
                    message = message,
                    onConfirm = { continuation.resume(true) },
                    onCancel = { continuation.resume(false) }
                )
            )
        }
    }

    @Composable
    override fun render(params: ConfirmDialogParams, closeDialog: () -> Unit) {
        content {
            Text(
                params.message,
                // textAlign = TextAlign.Center,
                fontSize = 1.2.em,
                modifier = Modifier.fillMaxWidth().padding(start = 30.dp, end = 30.dp)
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
    val message: String,
    val onConfirm: () -> Unit,
    val onCancel: () -> Unit = {}
)
