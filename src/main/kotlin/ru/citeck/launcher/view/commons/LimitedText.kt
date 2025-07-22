package ru.citeck.launcher.view.commons

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.*
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.*

@Composable
fun LimitedText(
    text: String,
    modifier: Modifier = Modifier,
    minWidth: Dp = Dp.Unspecified,
    maxWidth: Dp,
    color: Color = Color.Unspecified,
    fontSize: TextUnit = TextUnit.Unspecified,
) {
    CiteckTooltipArea(text) {
        Text(
            text = text,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = modifier.requiredWidthIn(min = minWidth, max = maxWidth),
            color = color,
            fontSize = fontSize
        )
    }
}
