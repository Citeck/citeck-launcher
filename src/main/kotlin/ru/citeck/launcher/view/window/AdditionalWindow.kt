package ru.citeck.launcher.view.window

import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.SnapshotStateList

@Composable
fun <T : Any> AdditionalWindow(
    statesList: SnapshotStateList<AdditionalWindowState>,
    content: @Composable (params: T, closeDialog: () -> Unit) -> Unit
): (T, MutableState<Boolean>) -> (() -> Unit) {

    val showWindow: (T, MutableState<Boolean>) -> (() -> Unit) = remember {
        { params, visible -> AdditionalWindowState.push(statesList, params, visible, content) }
    }

    return showWindow
}

class AdditionalWindowState(
    val content: @Composable () -> Unit,
    val visible: MutableState<Boolean>,
    val closeWindow: () -> Unit,
    val params: Any
) {
    companion object {
        internal fun <T : Any> push(
            states: MutableList<AdditionalWindowState>,
            params: T,
            visible: MutableState<Boolean>,
            content: @Composable (params: T, closeDialog: () -> Unit) -> Unit
        ): () -> Unit {
            val closeWindow: () -> Unit = {
                visible.value = false
                var lastVisibleWindowIdx = states.lastIndex
                while (lastVisibleWindowIdx >= 0 && !states[lastVisibleWindowIdx].visible.value) {
                    lastVisibleWindowIdx--
                }
                if (lastVisibleWindowIdx < 0) {
                    states.clear()
                } else {
                    while (lastVisibleWindowIdx < states.lastIndex) {
                        states.removeLast()
                    }
                }
            }
            states.add(
                AdditionalWindowState(
                    { content.invoke(params, closeWindow) },
                    visible,
                    closeWindow,
                    params
                )
            )
            return closeWindow
        }
    }
}
