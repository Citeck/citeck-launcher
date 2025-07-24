package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.*
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicLong

abstract class CiteckDialog<in T : Any> {

    companion object {

        val log = KotlinLogging.logger {}

        private val dialogStates: MutableList<DialogState<Any>> = mutableStateListOf()
        private val actionThreadIdx = AtomicLong()

        private fun <T : Any> pushDialog(dialog: CiteckDialog<T>, params: T): () -> Unit {

            var dialogState: DialogState<T>? = null

            val closeDialog: () -> Unit = {
                for (idx in dialogStates.lastIndex downTo 0) {
                    if (dialogStates[idx] === dialogState) {
                        dialogStates.removeAt(idx)
                        break
                    }
                }
            }
            dialogState = DialogState(
                dialog,
                params,
                closeDialog
            )
            @Suppress("UNCHECKED_CAST")
            dialogStates.add(dialogState as DialogState<Any>)

            return closeDialog
        }

        @Composable
        fun renderDialogs() {
            for (state in dialogStates) {
                state.dialog.render(state.params, state.closeDialog)
            }
        }

        val dialogActionExecutor: ExecutorService = Executors.newThreadPerTaskExecutor { job ->
            Thread.ofPlatform().name("dialog-action-" + actionThreadIdx.getAndIncrement()).unstarted(job)
        }
    }

    protected fun showDialog(params: T): () -> Unit {
        return pushDialog(this, params)
    }

    @Composable
    protected inline fun content(
        width: DialogWidth = DialogWidth.MEDIUM,
        crossinline render: @Composable DialogContext.() -> Unit
    ) {
        Dialog(
            properties = DialogProperties(usePlatformDefaultWidth = false),
            onDismissRequest = {}
        ) {
            Card(
                modifier = Modifier.width(width.dp),
                shape = RoundedCornerShape(10.dp),
                colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)
            ) {
                Column(modifier = Modifier.padding(top = 15.dp, start = 20.dp, end = 20.dp, bottom = 15.dp)) {
                    render(DialogContext(this))
                }
            }
        }
    }

    @Composable
    protected abstract fun render(params: T, closeDialog: () -> Unit)

    protected class DialogContext(private val columnScope: ColumnScope) : ColumnScope by columnScope {

        @Composable
        fun title(text: String) {
            Text(text, style = MaterialTheme.typography.titleLarge)
            Spacer(modifier = Modifier.height(15.dp))
        }

        @Composable
        inline fun buttonsRow(render: @Composable ButtonsRowContext.() -> Unit) {
            Spacer(modifier = Modifier.height(18.dp))
            Row(modifier = Modifier.height(40.dp)) {
                val buttonsEnabled = remember { mutableStateOf(true) }
                render.invoke(ButtonsRowContext(this, buttonsEnabled))
            }
        }
    }

    protected class ButtonsRowContext(
        private val rowScope: RowScope,
        val buttonsEnabled: MutableState<Boolean>
    ) : RowScope by rowScope {

        var firstBtn = true

        @Composable
        inline fun button(text: String, crossinline action: suspend () -> Unit) {
            Button(
                modifier = Modifier.padding(start = if (firstBtn) 0.dp else 10.dp).fillMaxHeight(),
                enabled = buttonsEnabled.value,
                onClick = {
                    if (buttonsEnabled.value) {
                        buttonsEnabled.value = false
                        dialogActionExecutor.submit {
                            try {
                                runBlocking {
                                    action.invoke()
                                }
                            } catch (e: Throwable) {
                                log.error(e) { "Button action completed exceptionally. Button: '$text'" }
                                ErrorDialog.show(e)
                            } finally {
                                buttonsEnabled.value = true
                            }
                        }
                    }
                }
            ) {
                Text(text)
            }
            firstBtn = false
        }

        @Composable
        fun spacer() {
            Spacer(modifier = Modifier.weight(1f))
        }
    }

    private class DialogState<T : Any>(
        val dialog: CiteckDialog<T>,
        val params: T,
        val closeDialog: () -> Unit
    )
}
