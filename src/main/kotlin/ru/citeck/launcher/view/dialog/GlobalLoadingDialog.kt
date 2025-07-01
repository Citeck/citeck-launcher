package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.window.Dialog

object GlobalLoadingDialog {

    private lateinit var showDialog: (Unit) -> (() -> Unit)

    fun show(): () -> Unit {
        return showDialog(Unit)
    }

    @Composable
    fun LoadingDialog(statesList: SnapshotStateList<CiteckDialogState>) {
        showDialog = CiteckDialog(statesList) { params, closeDialog ->
            Dialog(onDismissRequest = {}) {
                Card(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(30.dp),
                    shape = RoundedCornerShape(3.dp),
                ) {
                    Column(modifier = Modifier.padding(top = 30.dp, start = 10.dp, end = 10.dp, bottom = 30.dp)) {
                        Text(
                            "Loading...", textAlign = TextAlign.Center, fontSize = 1.2.em,
                            modifier = Modifier.fillMaxWidth().padding(start = 30.dp, end = 30.dp)
                        )
                    }
                }
            }
        }
    }
}
