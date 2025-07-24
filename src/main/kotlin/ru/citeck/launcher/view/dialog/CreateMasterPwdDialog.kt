package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.*
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume

object CreateMasterPwdDialog : CiteckDialog<CreateMasterPwdParams>() {

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
    override fun render(params: CreateMasterPwdParams, closeDialog: () -> Unit) {
        content {
            title("Create your personal master password")
            Text(
                "This password will be used to protect your secrets used by the launcher.",
                /*textAlign = TextAlign.Center*/
            )

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
            buttonsRow {
                spacer()
                button("Confirm") { onSubmit() }
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
