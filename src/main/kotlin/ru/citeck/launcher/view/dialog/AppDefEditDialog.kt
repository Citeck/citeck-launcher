package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.utils.json.Yaml
import ru.citeck.launcher.view.commons.CiteckTooltipArea
import ru.citeck.launcher.view.dialog.AppDefEditDialog.EditParams
import ru.citeck.launcher.view.form.exception.FormCancelledException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

object AppDefEditDialog : CiteckDialog<EditParams>() {

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
    override fun render(params: EditParams, closeDialog: () -> Unit) {

        content(width = DialogWidth.EXTRA_LARGE) {
            val textFieldValue = remember {
                mutableStateOf(TextFieldValue(Yaml.toString(params.appDef)))
            }
            val lockedValue = remember { mutableStateOf(params.locked) }

            val focusRequester = remember { FocusRequester() }
            LaunchedEffect(Unit) { focusRequester.requestFocus() }
            TextField(
                value = textFieldValue.value,
                onValueChange = { value: TextFieldValue -> textFieldValue.value = value },
                minLines = 20,
                colors = TextFieldDefaults.colors(
                    focusedContainerColor = MaterialTheme.colorScheme.surface,
                    unfocusedContainerColor = MaterialTheme.colorScheme.surface
                ),
                modifier = Modifier
                    .fillMaxWidth()
                    .focusRequester(focusRequester)
                    .border(1.dp, Color.Gray)
                    .weight(1f),
            )

            buttonsRow {
                CiteckTooltipArea("Without lock all changes will be lost after next Update&Start action") {
                    Row(modifier = Modifier.fillMaxHeight().align(Alignment.CenterVertically)) {
                        Checkbox(
                            onCheckedChange = { lockedValue.value = it },
                            checked = lockedValue.value,
                            modifier = Modifier.align(Alignment.CenterVertically)
                        )
                        Text("Lock changes", modifier = Modifier.align(Alignment.CenterVertically))
                    }
                }
                spacer()
                button("Reset") {
                    params.onReset()
                    closeDialog()
                }
                button("Cancel") {
                    params.onCancel()
                    closeDialog()
                }
                button("Submit") {
                    val editedApp = Yaml.read(
                        textFieldValue.value.text,
                        ApplicationDef::class
                    )
                    params.onSubmit(EditResponse(editedApp, lockedValue.value))
                    closeDialog()
                }
            }
        }
    }

    class EditResponse(
        val appDef: ApplicationDef,
        val locked: Boolean
    )

    class EditParams(
        val appDef: ApplicationDef,
        val locked: Boolean,
        val onCancel: () -> Unit,
        val onSubmit: (EditResponse) -> Unit,
        val onReset: () -> Unit
    )
}
