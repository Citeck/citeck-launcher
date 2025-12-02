package ru.citeck.launcher.view.commons.dialog

import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.suspendCancellableCoroutine
import org.apache.commons.lang3.exception.ExceptionUtils
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.view.popup.CiteckDialog
import ru.citeck.launcher.view.popup.DialogWidth
import ru.citeck.launcher.view.utils.SystemDumpUtils
import java.util.concurrent.CancellationException
import kotlin.coroutines.resume
import kotlin.math.min

class ErrorDialog(private val params: Params) : CiteckDialog() {

    companion object {
        val log = KotlinLogging.logger {}

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
                if (exception is CancellationException) {
                    log.debug { "Safe action was cancelled. Message: ${errorMsg()}" }
                } else {
                    log.error(exception) { errorMsg() }
                    show(exception)
                }
            } else {
                try {
                    @Suppress("UNCHECKED_CAST")
                    onSuccess(res as T)
                } catch (exception: Throwable) {
                    log.error(exception) { "onSuccess failed. " + errorMsg() }
                    show(exception)
                }
            }
        }

        suspend fun showSuspend(error: Throwable) {
            return suspendCancellableCoroutine { continuation ->
                show(error) { continuation.resume(Unit) }
            }
        }

        fun show(error: Throwable) {
            show(error) {}
        }

        fun show(error: Throwable, onClose: () -> Unit) {
            showDialog(ErrorDialog(prepareParams(error, onClose)))
        }

        fun prepareParamsWithoutTrace(error: Throwable, onClose: () -> Unit = {}): Params {
            val rootCause = ExceptionUtils.getRootCause(error) ?: error
            val message = (rootCause.localizedMessage ?: "").ifBlank { rootCause::class.simpleName }
            if (message.isNullOrBlank()) {
                return prepareParams(error, onClose)
            }
            return Params(message, true, onClose)
        }

        fun prepareParams(error: Throwable, onClose: () -> Unit = {}): Params {
            val rootCause = ExceptionUtils.getRootCause(error)
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

            return Params(message.toString(), false, onClose)
        }
    }

    @Composable
    override fun render() {
        val width = if (params.withoutTrace) {
            DialogWidth.MEDIUM
        } else {
            DialogWidth.EXTRA_LARGE
        }
        dialog(width = width) {
            title("Exception occurred")
            SelectionContainer { Text(params.errMsg) }
            buttonsRow {
                spacer()
                button("Export System Info") {
                    SystemDumpUtils.dumpSystemInfo(AppDir.PATH.resolve("reports"))
                }
                button("Close") {
                    closeDialog()
                    params.onClose()
                }
            }
        }
    }

    class Params(
        val errMsg: String,
        val withoutTrace: Boolean,
        val onClose: () -> Unit
    )
}
