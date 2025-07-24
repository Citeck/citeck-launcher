package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.view.dialog.LoadingDialog.InnerParams
import ru.citeck.launcher.view.utils.rememberMutProp

object LoadingDialog : CiteckDialog<InnerParams>() {

    fun show(status: ActionStatus.Mut? = null): () -> Unit {
        return showDialog(InnerParams(status))
    }

    @Composable
    override fun render(params: InnerParams, closeDialog: () -> Unit) {
        content(width = DialogWidth.SMALL) {
            Box(modifier = Modifier.height(130.dp)) {
                Column(modifier = Modifier.align(Alignment.Center)) {
                    Text(
                        "Please, wait...",
                        textAlign = TextAlign.Center,
                        fontSize = 1.2.em,
                        modifier = Modifier.fillMaxWidth().padding(start = 30.dp, end = 30.dp)
                    )
                    if (params.status != null) {
                        val status = rememberMutProp(params.status)
                        if (status.value.message.isNotEmpty()) {
                            Spacer(Modifier.height(10.dp))
                            Text(
                                "(${status.value.progressInPercent}%) ${status.value.message}",
                                textAlign = TextAlign.Center,
                                fontSize = 1.2.em,
                                modifier = Modifier.fillMaxWidth().padding(start = 30.dp, end = 30.dp)
                            )
                        }
                    }
                }
            }
        }
    }

    class InnerParams(
        val status: ActionStatus.Mut?
    )
}
