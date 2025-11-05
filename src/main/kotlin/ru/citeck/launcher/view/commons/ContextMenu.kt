package ru.citeck.launcher.view.commons

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.*
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.unit.*
import androidx.compose.ui.window.Popup
import androidx.compose.ui.window.PopupPositionProvider
import androidx.compose.ui.window.PopupProperties
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.view.commons.dialog.ErrorDialog

object ContextMenu {

    private val items = mutableStateOf<List<Item>>(emptyList())
    val actionInProgress = mutableStateOf(false)

    private val dropDownOffset = mutableStateOf(IntOffset.Zero)
    private val dropDownShow = mutableStateOf(false)

    private fun isPressed(buttons: PointerButtons, button: Button): Boolean {
        return when (button) {
            Button.LMB -> buttons.isPrimaryPressed
            Button.RMB -> buttons.isSecondaryPressed
        }
    }

    @Composable
    fun render() {
        if (dropDownShow.value) {
            PopupInWindow(
                offset = dropDownOffset.value,
                onDismissRequest = { dropDownShow.value = false }
            ) {
                val itemsColumnLayout = remember { mutableStateOf<LayoutCoordinates?>(null) }
                Column(
                    modifier = Modifier
                        .background(MaterialTheme.colorScheme.surface)
                        .widthIn(min = 150.dp)
                        .border(1.dp, Color.Gray)
                        .onGloballyPositioned { itemsColumnLayout.value = it }
                ) {
                    val itemsValue = items.value
                    val minWidth = itemsColumnLayout.value?.size?.width?.dp ?: 50.dp
                    for (item in itemsValue) {
                        Box(
                            modifier = Modifier
                                .wrapContentSize()
                                .widthIn(min = minWidth)
                                .clickable {
                                    if (!actionInProgress.value) {
                                        actionInProgress.value = true
                                        Thread.ofPlatform().start {
                                            runBlocking {
                                                ErrorDialog.doActionSafe({
                                                    item.action.invoke()
                                                }, { "Action failed" }, {})
                                            }
                                            actionInProgress.value = false
                                        }
                                        dropDownShow.value = false
                                    }
                                }
                        ) {
                            Text(
                                text = item.name,
                                modifier = Modifier.padding(start = 10.dp, end = 10.dp, top = 4.dp, bottom = 4.dp)
                            )
                            Box(modifier = Modifier.height(1.dp).background(Color.Gray).widthIn(min = minWidth))
                        }
                    }
                }
            }
        }
    }

    @Composable
    fun Modifier.contextMenu(button: Button, items: List<Item>): Modifier {
        val globalPos = remember { mutableStateOf<LayoutCoordinates?>(null) }

        return this.onGloballyPositioned {
            globalPos.value = it
        }.pointerInput(items) {
            awaitPointerEventScope {
                while (true) {
                    val event = awaitPointerEvent()
                    val pointer = event.changes.firstOrNull()
                    if (actionInProgress.value) {
                        continue
                    }
                    if (event.type == PointerEventType.Press &&
                        isPressed(event.buttons, button) &&
                        pointer != null
                    ) {
                        ContextMenu.items.value = items
                        var position = pointer.position
                        globalPos.value?.let { layout ->
                            position = layout.localToWindow(position)
                        }
                        dropDownOffset.value = IntOffset(position.x.toInt(), position.y.toInt())
                        dropDownShow.value = true
                        pointer.consume()
                    }
                }
            }
        }
    }

    @Composable
    @OptIn(ExperimentalComposeUiApi::class)
    private inline fun PopupInWindow(
        offset: IntOffset,
        crossinline onDismissRequest: () -> Unit,
        crossinline content: @Composable () -> Unit
    ) {
        Popup(
            onDismissRequest = { onDismissRequest() },
            properties = PopupProperties(
                focusable = true,
                clippingEnabled = false,
                usePlatformInsets = false
            ),
            popupPositionProvider = remember(offset) {
                object : PopupPositionProvider {
                    override fun calculatePosition(
                        anchorBounds: IntRect,
                        windowSize: IntSize,
                        layoutDirection: LayoutDirection,
                        popupContentSize: IntSize
                    ): IntOffset {
                        val windowLimX = windowSize.width - 4
                        val windowLimY = windowSize.height - 4

                        var x = offset.x
                        var y = offset.y

                        if (x + popupContentSize.width > windowLimX) {
                            x = windowLimX - popupContentSize.width
                        }

                        if (y + popupContentSize.height > windowLimY) {
                            y = windowLimY - popupContentSize.height
                        }

                        x = x.coerceAtLeast(0)
                        y = y.coerceAtLeast(0)

                        return IntOffset(x, y)
                    }
                }
            }
        ) {
            content()
        }
    }

    enum class Button {
        LMB,
        RMB
    }

    class Item(
        val name: String,
        val action: suspend () -> Unit,
    )
}
