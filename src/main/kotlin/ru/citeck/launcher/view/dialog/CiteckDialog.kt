package ru.citeck.launcher.view.dialog

import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.SnapshotStateList

@Composable
fun <T : Any> CiteckDialog(
    statesList: SnapshotStateList<CiteckDialogState>,
    initParams: T? = null,
    content: @Composable (params: T, closeDialog: () -> Unit) -> Unit
): (T) -> (() -> Unit) {

    remember {
        if (initParams != null) {
            CiteckDialogState.push(statesList, initParams, content)
        }
    }

    val showDialog: (T) -> (() -> Unit) = remember {
        { params -> CiteckDialogState.push(statesList, params, content) }
    }

    return showDialog
}

class CiteckDialogState(
    val content: @Composable () -> Unit,
    val params: Any
) {
    companion object {
        internal fun <T : Any> push(
            states: MutableList<CiteckDialogState>,
            params: T,
            content: @Composable (params: T, closeDialog: () -> Unit) -> Unit
        ): () -> Unit {
            var dialogState: CiteckDialogState? = null
            val closeDialog: () -> Unit = {
                for (idx in states.lastIndex downTo 0) {
                    if (states[idx] === dialogState) {
                        states.removeAt(idx)
                        break
                    }
                }
            }
            dialogState = CiteckDialogState(
                { content.invoke(params, closeDialog) },
                params
            )
            states.add(dialogState)
            return closeDialog
        }
    }
}
