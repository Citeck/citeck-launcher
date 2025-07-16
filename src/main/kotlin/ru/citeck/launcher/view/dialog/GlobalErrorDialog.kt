package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.Button
import androidx.compose.material.Card
import androidx.compose.material.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import io.github.oshai.kotlinlogging.KotlinLogging
import org.apache.commons.lang3.exception.ExceptionUtils
import ru.citeck.launcher.view.utils.SystemDumpUtils
import kotlin.math.min

object GlobalErrorDialog {

    val log = KotlinLogging.logger {}

    private lateinit var showDialog: (Params) -> (() -> Unit)

    suspend inline fun <T> doActionSafe(
        crossinline action: suspend () -> T,
        crossinline errorMsg: () -> String,
        crossinline onSuccess: (T) -> Unit
    ) {
        val res: Any? = try {
            action.invoke()
        } catch (e: Throwable) {
            Result.failure<Any>(e)
        }
        if (res is Result<*> && res.isFailure) {
            val exception = res.exceptionOrNull() ?: RuntimeException("Unknown exception")
            log.error(exception) { errorMsg() }
            show(Params(exception) {})
        } else {
            try {
                @Suppress("UNCHECKED_CAST")
                onSuccess(res as T)
            } catch (exception: Throwable) {
                log.error(exception) { "onSuccess failed. " + errorMsg() }
                show(Params(exception) {})
            }
        }
    }

    fun show(params: Params) {
        showDialog(params)
    }

    @Composable
    fun ErrorDialog(
        statesList: SnapshotStateList<CiteckDialogState>,
        initParams: Params? = null
    ) {
        showDialog = CiteckDialog(statesList, initParams) { params, closeDialog ->

            val rootCause = ExceptionUtils.getRootCause(params.error)
            val message = StringBuilder()
            val stackTrace = ExceptionUtils.getRootCauseStackTrace(rootCause)
            for (idx in 0 until min(10, stackTrace.size)) {
                var line = stackTrace[idx].replace("\n", "")
                if (line.isBlank()) {
                    continue
                }
                if (idx > 0) {
                    message.append("\n").append(" ")
                } else {
                    val javaEx = rootCause::class.java
                    line = line.replace(javaEx.name, javaEx.simpleName)
                }
                message.append(line.replace("\t", "  "))
            }
            val vScrollState = rememberScrollState()

            Dialog(
                onDismissRequest = {},
                properties = DialogProperties(
                    dismissOnBackPress = false,
                    dismissOnClickOutside = false,
                    usePlatformDefaultWidth = false
                )
            ) {
                Card(
                    modifier = Modifier
                        .width(1200.dp)
                        .padding(30.dp)
                        .verticalScroll(vScrollState),
                    shape = RoundedCornerShape(3.dp),
                ) {
                    Column(modifier = Modifier.padding(top = 30.dp, start = 30.dp, end = 30.dp)) {
                        Row(modifier = Modifier.padding(bottom = 15.dp)) {
                            Text(text = "Exception occurred", fontSize = 1.1.em)
                        }
                        Row {
                            SelectionContainer { Text(message.toString()) }
                        }
                        Row(modifier = Modifier.align(Alignment.End)) {
                            Button(
                                onClick = {
                                    SystemDumpUtils.dumpSystemInfo()
                                },
                                modifier = Modifier.padding(top = 5.dp, bottom = 5.dp, start = 0.dp, end = 10.dp),
                            ) {
                                Text("Export System Info")
                            }
                            Spacer(modifier = Modifier.width(10.dp))
                            Button(
                                onClick = {
                                    closeDialog()
                                    params.onClose()
                                },
                                modifier = Modifier.padding(top = 5.dp, bottom = 5.dp, start = 0.dp, end = 10.dp),
                            ) {
                                Text("Close")
                            }
                        }
                    }
                }
            }
        }
    }

    class Params(
        val error: Throwable,
        val onClose: () -> Unit
    )
}
