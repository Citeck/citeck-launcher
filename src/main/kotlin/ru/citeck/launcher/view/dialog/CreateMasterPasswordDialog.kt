package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.*
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume

object CreateMasterPasswordDialog {

    private lateinit var showDialog: (CreateMasterPwdParams) -> (() -> Unit)

    fun show(onSubmit: (CharArray) -> Boolean) {
        showDialog(CreateMasterPwdParams(onSubmit))
    }

    suspend fun showSuspend(): CharArray {
        return suspendCancellableCoroutine { continuation ->
            show {
                continuation.resume(it)
                true
            }
        }
    }

    @Composable
    fun CreateMasterPwd(statesList: SnapshotStateList<CiteckDialogState>) {

        showDialog = CiteckDialog(statesList) { params, closeDialog ->

            Dialog(
                properties = DialogProperties(
                    usePlatformDefaultWidth = false),
                onDismissRequest = {}
            ) {
                Surface(
                    shape = RoundedCornerShape(10.dp),
                    tonalElevation = 0.dp,
                    modifier = Modifier.width(600.dp).padding(5.dp)
                ) {
                    Column(modifier = Modifier.padding(top = 10.dp, bottom = 10.dp, start = 20.dp, end = 20.dp)) {
                        Text(
                            "Create your personal master password",
                            textAlign = TextAlign.Center,
                            fontSize = 1.2.em,
                            modifier = Modifier.fillMaxWidth(),
                            style = MaterialTheme.typography.titleLarge
                        )
                        Spacer(modifier = Modifier.height(10.dp))

                        Text(
                            "This password will be used to protect your secrets used by the launcher.",
                            textAlign = TextAlign.Center
                        )

                        Spacer(modifier = Modifier.height(16.dp))
                        val coroutineScope = rememberCoroutineScope()

                        val fields = remember { arrayOf(PwdField(), PwdField()) }
                        fun onSubmit() {
                            if (fields[0].value.value != fields[1].value.value) {
                                coroutineScope.launch {
                                    GlobalMessageDialog.show("Passwords do not match!")
                                }
                            } else if (fields[0].value.value.isBlank()) {
                                coroutineScope.launch {
                                    GlobalMessageDialog.show("Password is empty!")
                                }
                            } else {
                                if (params.onSubmit(fields[0].value.value.toCharArray())) {
                                    closeDialog()
                                }
                            }
                        }

                        for ((idx, field) in fields.withIndex()) {
                            val mutValue = field.value
                            val visible = field.visible
                            if (idx > 0) {
                                Spacer(modifier = Modifier.height(10.dp))
                            }
                            OutlinedTextField(
                                value = mutValue.value,
                                onValueChange = { mutValue.value = it },
                                singleLine = true,
                                visualTransformation = if (visible.value) VisualTransformation.None else PasswordVisualTransformation(),
                                trailingIcon = {
                                    val icon = if (visible.value) Icons.Default.Visibility else Icons.Default.VisibilityOff
                                    IconButton(onClick = { visible.value = !visible.value }) {
                                        Icon(icon, contentDescription = null)
                                    }
                                },
                                modifier = Modifier.fillMaxWidth().onPreviewKeyEvent { event ->
                                    if (event.key == Key.Enter && event.type == KeyEventType.KeyUp) {
                                        onSubmit()
                                        true
                                    } else {
                                        false
                                    }
                                }
                            )
                        }

                        Spacer(modifier = Modifier.height(20.dp))
                        Column(modifier = Modifier.fillMaxWidth().height(50.dp), horizontalAlignment = Alignment.End) {
                            Row {
                                Spacer(modifier = Modifier.weight(1f))
                                Button(
                                    onClick = { onSubmit() },
                                    modifier = Modifier.padding(top = 5.dp, bottom = 5.dp, start = 0.dp)
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

internal class PwdField(
    val visible: MutableState<Boolean> = mutableStateOf(false),
    val value: MutableState<String> = mutableStateOf("")
)

class CreateMasterPwdParams(
    val onSubmit: (CharArray) -> Boolean
)
