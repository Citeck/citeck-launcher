package ru.citeck.launcher.view.commons

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.TooltipArea
import androidx.compose.foundation.TooltipPlacement
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.DpOffset
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntRect
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.PopupPositionProvider

@Composable
fun CiteckTooltipArea(
    tooltip: String,
    enabled: Boolean = true,
    modifier: Modifier = Modifier,
    delayMillis: Int = 600,
    content: @Composable () -> Unit
) {
    @OptIn(ExperimentalFoundationApi::class)
    TooltipArea(
        delayMillis = delayMillis,
        modifier = modifier,
        tooltipPlacement = CustomTooltipPlacement,
        tooltip = {
            if (enabled && tooltip.isNotEmpty()) {
                Surface(shadowElevation = 4.dp, shape = RoundedCornerShape(4.dp)) {
                    Text(
                        text = tooltip,
                        modifier = Modifier.padding(8.dp)
                    )
                }
            }
        },
        content = content
    )
}

@OptIn(ExperimentalFoundationApi::class)
object CustomTooltipPlacement : TooltipPlacement {
    @Composable
    override fun positionProvider(cursorPosition: Offset): PopupPositionProvider {
        val offsetPx = with(LocalDensity.current) {
            val offset = DpOffset(0.dp, 8.dp)
            IntOffset(offset.x.roundToPx(), offset.y.roundToPx())
        }
        return remember {
            object : PopupPositionProvider {
                override fun calculatePosition(
                    anchorBounds: IntRect,
                    windowSize: IntSize,
                    layoutDirection: LayoutDirection,
                    popupContentSize: IntSize
                ): IntOffset {
                    val isTopTooltip = anchorBounds.top > windowSize.height / 2
                    val alignment = if (isTopTooltip) {
                        Alignment.TopCenter
                    } else {
                        Alignment.BottomCenter
                    }
                    val anchorPoint = alignment.align(IntSize.Zero, anchorBounds.size, layoutDirection)
                    val tooltipArea = IntRect(
                        IntOffset(
                            anchorBounds.left + anchorPoint.x - popupContentSize.width,
                            anchorBounds.top + anchorPoint.y - popupContentSize.height,
                        ),
                        IntSize(
                            popupContentSize.width * 2,
                            popupContentSize.height * 2
                        )
                    )
                    val position = alignment.align(popupContentSize, tooltipArea.size, layoutDirection)
                    return tooltipArea.topLeft + position + if (isTopTooltip) (-offsetPx) else offsetPx
                }
            }
        }
    }
}
