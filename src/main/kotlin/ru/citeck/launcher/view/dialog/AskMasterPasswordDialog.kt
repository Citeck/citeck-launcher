package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.*
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation

object AskMasterPasswordDialog : CiteckDialog<AskMasterPwdParams>() {

    fun show(onSubmit: (CharArray) -> Boolean, onReset: () -> Unit) {
        showDialog(AskMasterPwdParams(onSubmit, onReset))
    }

    @Composable
    override fun render(params: AskMasterPwdParams, closeDialog: () -> Unit) {
        content {
            title("Enter your personal master password")

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

            buttonsRow {
                button("Reset Master Password and Drop All Secrets") {
                    ConfirmDialog.show(
                        "Are you sure?" +
                            "\nAll your secrets will be deleted from local storage"
                    ) {
                        params.onReset()
                        closeDialog()
                    }
                }
                spacer()
                button("Confirm") {
                    if (params.onSubmit(value.value.toCharArray())) {
                        closeDialog()
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
