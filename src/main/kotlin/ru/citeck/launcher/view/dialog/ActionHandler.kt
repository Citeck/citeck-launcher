package ru.citeck.launcher.view.dialog

import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

val log = KotlinLogging.logger {}

class ErrorBoundary(
    private val showLoadingDialog: (Unit) -> (() -> Unit),
    private val coroutineScope: CoroutineScope
) {

    fun <T> runAction(action: suspend () -> Result<T>,
                      onSuccess: (T) -> Unit = {},
                      onFailure: (Throwable) -> Unit = {},
                      finally: () -> Unit = {}
    ) {
        val closeLoadingDialog = showLoadingDialog(Unit)
        coroutineScope.launch {
            val res = action.invoke()
            closeLoadingDialog()
            if (res.isFailure) {
                val error = res.exceptionOrNull()!!
                log.error(error) { "Action completed with error" }
                GlobalErrorDialog.show(GlobalErrorDialog.Params(error) {
                    onFailure.invoke(error)
                })
            } else {
                onSuccess.invoke(res.getOrNull()!!)
            }
            finally.invoke()
        }
    }
}
