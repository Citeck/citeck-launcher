package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.view.commons.dialog.GlobalMsgDialogParams
import ru.citeck.launcher.view.commons.dialog.MessageDialog
import ru.citeck.launcher.view.popup.CiteckDialog
import ru.citeck.launcher.view.popup.DialogWidth
import java.nio.file.Path
import kotlin.coroutines.resume
import kotlin.io.path.exists

class CreateOrEditSnapshotDialog(
    private val params: InternalParams
) : CiteckDialog() {

    companion object {
        private val VALID_NAME_REGEX = "[\\w-.]+".toRegex()

        suspend fun showCreate(baseDirPath: Path, name: String): String {
            return suspendCancellableCoroutine { continuation ->
                showDialog(
                    CreateOrEditSnapshotDialog(
                        InternalParams(baseDirPath, editMode = false, name) {
                            continuation.resume(it)
                        }
                    )
                )
            }
        }

        suspend fun showEdit(name: String): String {
            return suspendCancellableCoroutine { continuation ->
                showDialog(
                    CreateOrEditSnapshotDialog(
                        InternalParams(null, editMode = true, name) {
                            continuation.resume(it)
                        }
                    )
                )
            }
        }
    }

    @Composable
    override fun render() {
        val snapshotName = remember { mutableStateOf(params.name) }
        dialog(width = DialogWidth.MEDIUM) {
            if (params.editMode) {
                title("Edit Snapshot")
            } else {
                title("Create New Snapshot")
            }
            Text("Snapshot name")
            OutlinedTextField(
                value = snapshotName.value,
                onValueChange = { snapshotName.value = it },
                singleLine = true,
                modifier = Modifier.fillMaxWidth()
            )
            buttonsRow {
                button("Cancel") {
                    closeDialog()
                    params.onClose("")
                }
                spacer()
                button(if (params.editMode) "Save" else "Create", enabledIf = { snapshotName.value.isNotBlank() }) {
                    var snapshotNameValue = snapshotName.value.trim()
                    if (snapshotNameValue.endsWith(".zip")) {
                        snapshotNameValue = snapshotNameValue.substringBeforeLast(".")
                    }
                    if (!snapshotNameValue.matches(VALID_NAME_REGEX)) {
                        MessageDialog.show(
                            GlobalMsgDialogParams(
                                "Invalid snapshot name",
                                "Name '$snapshotNameValue' doesn't allowed\n" +
                                    "Please, enter name using characters, digits, dots, dash or underscore."
                            )
                        )
                    } else {
                        val nameWithZip = "$snapshotNameValue.zip"
                        if (params.baseDirPath?.resolve(nameWithZip)?.exists() == true) {
                            MessageDialog.show(
                                GlobalMsgDialogParams(
                                    "Already exists",
                                    "Snapshot with this name already exists. Please, enter other name."
                                )
                            )
                        } else {
                            closeDialog()
                            params.onClose(snapshotNameValue)
                        }
                    }
                }
            }
        }
    }

    class InternalParams(
        val baseDirPath: Path?,
        val editMode: Boolean,
        val name: String,
        val onClose: (String) -> Unit
    )
}
