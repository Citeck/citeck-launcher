package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog

object GlobalSnapshotsDialog {

    //
    private lateinit var showDialog: (GlobalMsgDialogParams) -> (() -> Unit)

    suspend fun show(text: String, title: String = "") {
        showDialog(GlobalMsgDialogParams(title, text))
    }

    suspend fun show(params: GlobalMsgDialogParams) {
        showDialog(params)
    }

    @Composable
    fun SnapshotsDialog(statesList: SnapshotStateList<CiteckDialogState>) {
/*
        showDialog = CiteckDialog(statesList) { params, closeDialog ->

            Dialog(onDismissRequest = {}) {
                Surface(
                    shape = RoundedCornerShape(10.dp),
                    tonalElevation = 0.dp,
                    modifier = Modifier.fillMaxWidth().padding(5.dp)
                ) {
                    Column(modifier = Modifier.padding(15.dp)) {
                        if (params.title.isNotBlank()) {
                            Text(
                                text = params.title, //"Create your personal master password",
                                style = MaterialTheme.typography.titleLarge
                            )
                        }
                        Spacer(modifier = Modifier.height(8.dp))
                        Text(
                            text = params.text,
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                        Spacer(modifier = Modifier.height(16.dp))
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.End
                        ) {
                            Button(
                                onClick = {
                                    closeDialog()
                                }
                            ) {
                                Text("Ok")
                            }
                        }
                    }
                }
            }
        }*/
    }
}



