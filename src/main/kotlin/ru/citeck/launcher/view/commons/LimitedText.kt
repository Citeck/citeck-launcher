package ru.citeck.launcher.view.commons

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.TooltipArea
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.*
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.*

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun LimitedText(
    text: String,
    modifier: Modifier = Modifier,
    maxWidth: Dp,
    color: Color = Color.Unspecified,
    fontSize: TextUnit = TextUnit.Unspecified,
) {
    TooltipArea(
        tooltip = {
            Surface(shadowElevation = 4.dp, shape = RoundedCornerShape(4.dp)) {
                Text(text = text, modifier = Modifier.padding(8.dp))
            }
        }
    ) {
        Text(
            text = text,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = modifier.width(maxWidth),
            color = color,
            fontSize = fontSize
        )
    }
}
