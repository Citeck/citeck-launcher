package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.Button
import androidx.compose.material.Card
import androidx.compose.material.Text
import androidx.compose.material.TextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogWindow
import androidx.compose.ui.window.WindowPosition
import androidx.compose.ui.window.rememberDialogState

@Composable
fun EditMultilineTextDialog(statesList: SnapshotStateList<CiteckDialogState>): (EditMultilineTextParams) -> (() -> Unit) {

    return CiteckDialog(statesList) { params, closeDialog ->

        val textFieldValue = remember(params.text) { mutableStateOf(TextFieldValue(params.text)) }

        val state = rememberDialogState(
            width = 1000.dp,
            height = 800.dp,
            position = WindowPosition(Alignment.Center)
        )

        DialogWindow(
            onCloseRequest = {
                params.cancelAction.invoke()
                closeDialog()
            },
            title = "Editor",
            state = state
        ) {
            //val errorBoundary = UserActionHandler()
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
                        modifier = Modifier
                            .fillMaxWidth()
                            .weight(1f)
                    )
                    Row(
                        modifier = Modifier
                            .height(50.dp)
                            .fillMaxWidth(),
                        horizontalArrangement = Arrangement.End
                    ) {
                        for (button in params.buttons) {
                            Button(
                                onClick = {
                                   // errorBoundary.runAction({
                                   //     button.action.invoke(textFieldValue.value.text)
                                   // },
                                    //    onSuccess = { closeDialog() }
                                   // )
                                },
                                modifier = Modifier.padding(top = 5.dp, bottom = 5.dp, start = 0.dp, end = 10.dp)
                                    .fillMaxHeight()
                            ) {
                                Text(button.text)
                            }
                        }
                    }
                }
            }
        }

    }
}

class EditMultilineTextParams(
    val text: String,
    val buttons: List<DialogButtonDesc>,
    val cancelAction: () -> Unit
)

class DialogButtonDesc(
    val text: String,
    val action: suspend (String) -> Result<Unit>
)
