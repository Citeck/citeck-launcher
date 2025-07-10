package ru.citeck.launcher.view.utils

import androidx.compose.runtime.*
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

private class MutablePropView<T, R>(
    private val prop: MutProp<T>,
    private val conv: (T) -> R
) : RememberObserver {

    val state = mutableStateOf(conv(prop.value))
    @Volatile
    private var listenerHandle: Disposable? = null
    private val watcher = Watcher()

    @Synchronized
    override fun onRemembered() {
        listenerHandle?.dispose()
        listenerHandle = prop.watch(watcher)
        state.value = conv(prop.value)
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
