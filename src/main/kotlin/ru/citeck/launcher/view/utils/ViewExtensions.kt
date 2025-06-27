package ru.citeck.launcher.view.utils

import androidx.compose.runtime.*
import ru.citeck.launcher.core.utils.Disposable
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

class MutablePropView<T, R>(
    private val prop: MutProp<T>,
    private val conv: (T) -> R
) : RememberObserver {

    val state = mutableStateOf(conv(prop.value))
    private var listenerHandle: Disposable? = null

    override fun onRemembered() {
        listenerHandle = prop.watch { _, newVal ->
            state.value = conv(newVal)
        }
        state.value = conv(prop.value)
    }

    override fun onAbandoned() = onForgotten()
    override fun onForgotten() {
        listenerHandle?.dispose()
        listenerHandle = null
    }
}
