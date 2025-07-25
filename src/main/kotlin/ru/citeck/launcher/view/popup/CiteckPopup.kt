package ru.citeck.launcher.view.popup

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.view.commons.dialog.ErrorDialog
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicLong

abstract class CiteckPopup {

    companion object {

        val log = KotlinLogging.logger {}

        private val actionThreadIdx = AtomicLong()

        val popupActionExecutor: ExecutorService = Executors.newThreadPerTaskExecutor { job ->
            Thread.ofPlatform().name("popup-action-" + actionThreadIdx.getAndIncrement()).unstarted(job)
        }
    }

    protected open fun beforeClose() {}

    @Composable
    abstract fun render()

    protected class PopupContext(private val columnScope: ColumnScope) : ColumnScope by columnScope {

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
                        popupActionExecutor.submit {
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
}
