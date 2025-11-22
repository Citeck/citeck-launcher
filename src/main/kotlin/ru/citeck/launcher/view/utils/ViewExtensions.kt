package ru.citeck.launcher.view.utils

import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.IdUtils
import ru.citeck.launcher.core.utils.prop.MutProp

@Composable
fun <T> rememberMutProp(prop: MutProp<T>): MutableState<T> {
    return rememberMutProp(prop) { it }
}

@Composable
fun <T> rememberMutProp(key0: Any?, prop: MutProp<T>): MutableState<T> {
    return rememberMutProp(key0, prop) { it }
}

@Composable
fun <T, R> rememberMutProp(prop: MutProp<T>, conv: (T) -> R): MutableState<R> {
    val view = remember { MutablePropView(prop, conv) }
    return view.state
}

@Composable
fun <T, R> rememberMutProp(key0: Any?, prop: MutProp<T>, conv: (T) -> R): MutableState<R> {
    rememberCoroutineScope()
    val view = remember(key0) { MutablePropView(prop, conv) }
    return view.state
}

inline fun Modifier.onEnterClick(crossinline action: () -> Unit): Modifier {
    return this.onPreviewKeyEvent { event ->
        if ((event.key == Key.Enter || event.key == Key.NumPadEnter) && event.type == KeyEventType.KeyUp) {
            action()
            true
        } else {
            false
        }
    }
}

private class MutablePropView<T, R>(
    private val prop: MutProp<T>,
    private val conv: (T) -> R
) : RememberObserver {

    val state = mutableStateOf(conv(prop.getValue()))
    @Volatile
    private var listenerHandle: Disposable? = null
    private val watcher = Watcher()

    @Synchronized
    override fun onRemembered() {
        listenerHandle?.dispose()
        listenerHandle = prop.watch(watcher)
        state.value = conv(prop.getValue())
    }

    override fun onAbandoned() = onForgotten()

    @Synchronized
    override fun onForgotten() {
        listenerHandle?.dispose()
        listenerHandle = null
    }

    private inner class Watcher : Function2<T, T, Unit> {

        val id = IdUtils.createStrId()

        override fun invoke(before: T, after: T) {
            state.value = conv(after)
        }

        override fun toString(): String {
            return "ViewWatcher[$id:$prop]"
        }
    }
}
