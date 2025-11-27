package ru.citeck.launcher.view.commons

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.TooltipArea
import androidx.compose.foundation.TooltipPlacement
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.DpOffset
import androidx.compose.ui.unit.dp

@Composable
fun CiteckTooltipArea(
    tooltip: String,
    enabled: Boolean = true,
    modifier: Modifier = Modifier,
    delayMillis: Int = 600,
    placement: CiteckTooltipPlacement = CiteckTooltipPlacement.CURSOR,
    content: @Composable () -> Unit
) {
    @OptIn(ExperimentalFoundationApi::class)
    val tooltipPlacement = when (placement) {
        CiteckTooltipPlacement.CURSOR -> TooltipPlacement.CursorPoint(
            offset = DpOffset((-16).dp, 16.dp)
        )
        CiteckTooltipPlacement.TOP -> TooltipPlacement.ComponentRect(
            anchor = Alignment.TopCenter,
            alignment = Alignment.TopCenter,
            offset = DpOffset(0.dp, (-8).dp)
        )
    }

    @OptIn(ExperimentalFoundationApi::class)
    TooltipArea(
        delayMillis = delayMillis,
        modifier = modifier,
        tooltipPlacement = tooltipPlacement,
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

enum class CiteckTooltipPlacement {
    CURSOR,
    TOP
}
