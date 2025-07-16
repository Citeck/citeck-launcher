package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.window.Dialog
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume

object GlobalConfirmDialog {

    private lateinit var showDialog: (ConfirmDialogParams) -> (() -> Unit)

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
    fun ConfirmDialog(statesList: SnapshotStateList<CiteckDialogState>) {
        showDialog = CiteckDialog(statesList) { params, closeDialog ->
            Dialog(onDismissRequest = {}) {
                Card(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(30.dp),
                    shape = RoundedCornerShape(3.dp),
                ) {
                    Column(modifier = Modifier.padding(top = 30.dp, start = 10.dp, end = 10.dp, bottom = 10.dp)) {
                        Text(
                            params.message,
                            textAlign = TextAlign.Center,
                            fontSize = 1.2.em,
                            modifier = Modifier.fillMaxWidth().padding(start = 30.dp, end = 30.dp)
                        )
                        Spacer(modifier = Modifier.height(30.dp))
                        Column(modifier = Modifier.fillMaxWidth().height(50.dp), horizontalAlignment = Alignment.End) {
                            Row {
                                Button(
                                    onClick = {
                                        closeDialog()
                                        params.onCancel()
                                    },
                                    modifier = Modifier.padding(top = 5.dp, bottom = 5.dp, start = 0.dp, end = 10.dp)
                                        .fillMaxHeight()
                                ) {
                                    Text("Cancel")
                                }
                                Button(
                                    onClick = {
                                        closeDialog()
                                        params.onConfirm()
                                    },
                                    modifier = Modifier.padding(top = 5.dp, bottom = 5.dp, start = 0.dp/*, end = 10.dp*/)
                                        .fillMaxHeight()
                                ) {
                                    Text("Confirm")
                                }
                            }
                        }
                    }
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
