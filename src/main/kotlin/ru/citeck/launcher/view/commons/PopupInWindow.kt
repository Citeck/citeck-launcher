package ru.citeck.launcher.view.commons

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntRect
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.window.Popup
import androidx.compose.ui.window.PopupPositionProvider
import androidx.compose.ui.window.PopupProperties

const val POPUP_IN_WINDOW_PADDING = 10

@Composable
@OptIn(ExperimentalComposeUiApi::class)
inline fun PopupInWindow(
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
                    val windowLimX = windowSize.width - POPUP_IN_WINDOW_PADDING
                    val windowLimY = windowSize.height - POPUP_IN_WINDOW_PADDING

                    var x = offset.x
                    var y = offset.y

                    if (x + popupContentSize.width > windowLimX) {
                        x = windowLimX - popupContentSize.width
                    }

                    if (y + popupContentSize.height > windowLimY) {
                        y = windowLimY - popupContentSize.height
                    }

                    x = x.coerceAtLeast(POPUP_IN_WINDOW_PADDING)
                    y = y.coerceAtLeast(POPUP_IN_WINDOW_PADDING)

                    return IntOffset(x, y)
                }
            }
        }
    ) {
        content()
    }
}
