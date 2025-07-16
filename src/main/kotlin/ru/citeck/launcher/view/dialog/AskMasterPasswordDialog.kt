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
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.*
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties

object AskMasterPasswordDialog {

    private lateinit var showDialog: (AskMasterPwdParams) -> (() -> Unit)

    fun show(onSubmit: (CharArray) -> Boolean, onReset: () -> Unit) {
        showDialog(AskMasterPwdParams(onSubmit, onReset))
    }

    @Composable
    fun AskMasterPwd(statesList: SnapshotStateList<CiteckDialogState>) {

        showDialog = CiteckDialog(statesList) { params, closeDialog ->

            Dialog(
                properties = DialogProperties(
                    usePlatformDefaultWidth = false
                ),
                onDismissRequest = {}
            ) {
                Surface(
                    shape = RoundedCornerShape(10.dp),
                    tonalElevation = 0.dp,
                    modifier = Modifier.width(600.dp).padding(5.dp)
                ) {
                    Column(modifier = Modifier.padding(top = 10.dp, bottom = 10.dp, start = 20.dp, end = 20.dp)) {
                        Text(
                            "Enter your personal master password",
                            textAlign = TextAlign.Center,
                            fontSize = 1.2.em,
                            modifier = Modifier.fillMaxWidth(),
                            style = MaterialTheme.typography.titleLarge
                        )

                        Spacer(modifier = Modifier.height(16.dp))

                        var passwordVisible by remember { mutableStateOf(false) }
                        val value = remember { mutableStateOf("") }
                        val focusRequester = remember { FocusRequester() }
                        LaunchedEffect(Unit) {
                            focusRequester.requestFocus()
                        }
                        OutlinedTextField(
                            value = value.value,
                            onValueChange = { value.value = it },
                            singleLine = true,
                            visualTransformation = if (passwordVisible) VisualTransformation.None else PasswordVisualTransformation(),
                            trailingIcon = {
                                val icon = if (passwordVisible) Icons.Default.Visibility else Icons.Default.VisibilityOff
                                IconButton(onClick = { passwordVisible = !passwordVisible }) {
                                    Icon(icon, contentDescription = null)
                                }
                            },
                            modifier = Modifier.fillMaxWidth().focusRequester(focusRequester).onPreviewKeyEvent { event ->
                                if (event.key == Key.Enter && event.type == KeyEventType.KeyUp) {
                                    if (params.onSubmit(value.value.toCharArray())) {
                                        closeDialog()
                                    }
                                    true
                                } else {
                                    false
                                }
                            }
                        )
                        Spacer(modifier = Modifier.height(20.dp))
                        Column(modifier = Modifier.fillMaxWidth().height(50.dp), horizontalAlignment = Alignment.End) {
                            Row {
                                Button(
                                    onClick = {
                                        GlobalConfirmDialog.show(
                                            "Are you sure?" +
                                                "\nAll your secrets will be deleted from local storage"
                                        ) {
                                            params.onReset()
                                            closeDialog()
                                        }
                                    },
                                    modifier = Modifier.padding(top = 5.dp, bottom = 5.dp, start = 0.dp, end = 10.dp)
                                        .fillMaxHeight()
                                ) {
                                    Text("Reset Master Password and Drop All Secrets")
                                }
                                Spacer(modifier = Modifier.weight(1f))
                                Button(
                                    onClick = {
                                        if (params.onSubmit(value.value.toCharArray())) {
                                            closeDialog()
                                        }
                                    },
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

class AskMasterPwdParams(
    val onSubmit: (CharArray) -> Boolean,
    val onReset: () -> Unit,
)
