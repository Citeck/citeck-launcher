package ru.citeck.launcher.view.commons.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.view.popup.CiteckDialog
import ru.citeck.launcher.view.popup.DialogWidth
import ru.citeck.launcher.view.utils.rememberMutProp

class LoadingDialog(private val params: InnerParams) : CiteckDialog() {

    companion object {
        fun show(status: ActionStatus.Mut? = null): () -> Unit {
            val dialog = showDialog(LoadingDialog(InnerParams(status)))
            return { dialog.closeDialog() }
        }
    }

    @Composable
    override fun render() {
        dialog(width = DialogWidth.SMALL) {
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
