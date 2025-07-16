@file:OptIn(ExperimentalFoundationApi::class)

package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.TooltipArea
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogWindow
import androidx.compose.ui.window.WindowPosition
import androidx.compose.ui.window.rememberDialogState
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.utils.json.Yaml
import ru.citeck.launcher.view.form.exception.FormCancelledException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

object AppDefEditDialog {

    private lateinit var showDialog: (EditParams) -> (() -> Unit)

    suspend fun show(appDef: ApplicationDef, locked: Boolean): EditResponse? {
        return suspendCancellableCoroutine { continuation ->
            showDialog(
                EditParams(
                    appDef,
                    locked,
                    { continuation.resumeWithException(FormCancelledException()) },
                    { continuation.resume(it) },
                    { continuation.resume(null) }
                )
            )
        }
    }

    @Composable
    fun EditDialog(statesList: SnapshotStateList<CiteckDialogState>) {

        showDialog = CiteckDialog(statesList) { params, closeDialog ->

            val textFieldValue = remember {
                mutableStateOf(TextFieldValue(Yaml.toString(params.appDef)))
            }
            val lockedValue = remember { mutableStateOf(params.locked) }

            val state = rememberDialogState(
                width = 1000.dp,
                height = 800.dp,
                position = WindowPosition(Alignment.Center)
            )

            DialogWindow(
                onCloseRequest = {
                    params.onCancel()
                    closeDialog()
                },
                title = "Edit ${params.appDef.name}",
                state = state
            ) {
                Card(
                    modifier = Modifier
                        .fillMaxWidth()
                        .fillMaxHeight()
                        .padding(0.dp),
                    shape = RoundedCornerShape(5.dp),
                ) {
                    Column(
                        modifier = Modifier
                            .fillMaxWidth()
                    ) {
                        TextField(
                            value = textFieldValue.value,
                            onValueChange = { value: TextFieldValue -> textFieldValue.value = value },
                            minLines = 20,
                            colors = TextFieldDefaults.colors(
                                focusedContainerColor = Color.White,
                                unfocusedContainerColor = Color.White
                            ),
                            modifier = Modifier
                                .fillMaxWidth()
                                .weight(1f),

                        )
                        Row(
                            modifier = Modifier
                                .height(50.dp)
                                .background(Color.White)
                                .fillMaxWidth()
                        ) {
                            TooltipArea(
                                delayMillis = 1000,
                                tooltip = {
                                    Surface(shadowElevation = 4.dp, shape = RoundedCornerShape(4.dp)) {
                                        Text(
                                            text = "Without lock all changes will be lost after next Update&Start action",
                                            modifier = Modifier.padding(8.dp)
                                        )
                                    }
                                }
                            ) {
                                Row(modifier = Modifier.fillMaxHeight().align(Alignment.CenterVertically)) {
                                    Checkbox(
                                        onCheckedChange = { lockedValue.value = it },
                                        checked = lockedValue.value,
                                        modifier = Modifier.align(Alignment.CenterVertically)
                                    )
                                    Text("Lock changes", modifier = Modifier.align(Alignment.CenterVertically))
                                }
                            }
                            Spacer(Modifier.weight(1f))
                            val buttonsEnabled = remember { mutableStateOf(true) }
                            Button(
                                enabled = buttonsEnabled.value,
                                modifier = Modifier.align(Alignment.CenterVertically),
                                onClick = {
                                    buttonsEnabled.value = false
                                    try {
                                        params.onReset()
                                        closeDialog()
                                    } catch (_: Throwable) {
                                        buttonsEnabled.value = true
                                    }
                                }
                            ) {
                                Text("Reset")
                            }
                            Spacer(Modifier.width(5.dp))
                            Button(
                                enabled = buttonsEnabled.value,
                                modifier = Modifier.align(Alignment.CenterVertically),
                                onClick = {
                                    buttonsEnabled.value = false
                                    try {
                                        params.onCancel()
                                        closeDialog()
                                    } catch (_: Throwable) {
                                        buttonsEnabled.value = true
                                    }
                                }
                            ) {
                                Text("Cancel")
                            }
                            Spacer(Modifier.width(5.dp))
                            val coroutineScope = rememberCoroutineScope()
                            Button(
                                enabled = buttonsEnabled.value,
                                modifier = Modifier.align(Alignment.CenterVertically),
                                onClick = {
                                    coroutineScope.launch {
                                        GlobalErrorDialog.doActionSafe(
                                            {
                                                buttonsEnabled.value = false
                                                try {
                                                    val editedApp = Yaml.read(
                                                        textFieldValue.value.text,
                                                        ApplicationDef::class
                                                    )
                                                    params.onSubmit(EditResponse(editedApp, lockedValue.value))
                                                    closeDialog()
                                                } finally {
                                                    buttonsEnabled.value = true
                                                }
                                            },
                                            { "Editing failed" },
                                            {}
                                        )
                                    }
                                }
                            ) {
                                Text("Submit")
                            }
                            Spacer(Modifier.width(10.dp))
                        }
                    }
                }
            }
        }
    }

    class EditResponse(
        val appDef: ApplicationDef,
        val locked: Boolean
    )

    private class EditParams(
        val appDef: ApplicationDef,
        val locked: Boolean,
        val onCancel: () -> Unit,
        val onSubmit: (EditResponse) -> Unit,
        val onReset: () -> Unit
    )
}
