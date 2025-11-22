package ru.citeck.launcher.view.popup

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.view.commons.dialog.ErrorDialog
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicLong

abstract class CiteckPopup(val kind: CiteckPopupKind) {

    companion object {

        val log = KotlinLogging.logger {}

        private val actionThreadIdx = AtomicLong()

        val popupActionExecutor: ExecutorService = Executors.newThreadPerTaskExecutor { job ->
            Thread.ofPlatform().name("popup-action-" + actionThreadIdx.getAndIncrement()).unstarted(job)
        }
    }

    val actionsEnabled: MutableState<Boolean> = mutableStateOf(true)

    protected open fun beforeClose() {}

    @Composable
    abstract fun render()

    inline fun executePopupAction(
        desc: String,
        crossinline action: suspend () -> Unit
    ) {
        if (!actionsEnabled.value) {
            return
        }
        actionsEnabled.value = false
        popupActionExecutor.submit {
            try {
                runBlocking {
                    action.invoke()
                }
            } catch (e: Throwable) {
                log.error(e) { "Popup action completed exceptionally. $desc" }
                ErrorDialog.show(e)
            } finally {
                actionsEnabled.value = true
            }
        }
    }

    protected inner class PopupContext(
        private val columnScope: ColumnScope
    ) : ColumnScope by columnScope {

        @Composable
        fun title(text: String) {
            Text(text, style = MaterialTheme.typography.titleLarge)
            Spacer(modifier = Modifier.height(15.dp))
        }

        @Composable
        inline fun buttonsRow(render: @Composable ButtonsRowContext.() -> Unit) {
            when (kind) {
                CiteckPopupKind.DIALOG -> {
                    Spacer(modifier = Modifier.height(18.dp))
                    Row(modifier = Modifier.height(40.dp)) {
                        render.invoke(ButtonsRowContext(this))
                    }
                    Spacer(modifier = Modifier.height(15.dp))
                }

                CiteckPopupKind.WINDOW -> {
                    Spacer(modifier = Modifier.height(5.dp))
                    Row(modifier = Modifier.height(40.dp).padding(start = 10.dp, end = 10.dp)) {
                        render.invoke(ButtonsRowContext(this))
                    }
                    Spacer(modifier = Modifier.height(5.dp))
                }
            }
        }
    }

    inner class ButtonsRowContext(
        private val rowScope: RowScope
    ) : RowScope by rowScope {

        var firstBtn = true

        @Composable
        inline fun button(text: String, crossinline action: suspend () -> Unit) {
            button(text, { true }, action)
        }

        @Composable
        inline fun button(text: String, crossinline enabledIf: () -> Boolean, crossinline action: suspend () -> Unit) {
            Button(
                modifier = Modifier.padding(start = if (firstBtn) 0.dp else 10.dp).fillMaxHeight(),
                enabled = actionsEnabled.value && enabledIf(),
                onClick = {
                    executePopupAction("Button: '$text'", action)
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
