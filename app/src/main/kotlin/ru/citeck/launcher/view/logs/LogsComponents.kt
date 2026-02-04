package ru.citeck.launcher.view.logs

import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Checkbox
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.platform.Font
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp

object LogLevelColors {
    val textColors: Map<LogLevel, Color> = mapOf(
        LogLevel.ERROR to Color(0xFFC62828),
        LogLevel.WARN to Color(0xFFF57C00),
        LogLevel.DEBUG to Color(0xFF757575),
        LogLevel.TRACE to Color(0xFF9E9E9E),
        LogLevel.INFO to Color(0xFF2E7D32),
        LogLevel.UNKNOWN to Color.Black
    )

    val highlightStyle = SpanStyle(
        background = Color.Yellow,
        color = Color.Black
    )

    val currentHighlightStyle = SpanStyle(
        background = Color(0xFFFF9800),
        color = Color.Black
    )
}

val logsFont: FontFamily = FontFamily(
    Font(
        resource = "fonts/ubuntu/UbuntuMono-R.ttf",
        weight = FontWeight.Normal,
        style = FontStyle.Normal
    )
)

@Composable
fun rememberLogsTextStyle(): TextStyle {
    return remember(logsFont) {
        TextStyle(
            fontFamily = logsFont,
            fontSize = 1.em,
            color = Color.Black
        )
    }
}

@Composable
fun CheckboxWithLabel(
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    text: String
) {
    Row(
        verticalAlignment = Alignment.CenterVertically
    ) {
        Checkbox(
            checked = checked,
            onCheckedChange = onCheckedChange,
            modifier = Modifier.size(16.dp)
        )
        Spacer(Modifier.width(4.dp))
        Text(
            text = text,
            fontSize = 0.7.em,
            modifier = Modifier.clickable { onCheckedChange(!checked) }
        )
    }
}

@Composable
fun CompactSearchField(
    value: String,
    onValueChange: (String) -> Unit,
    modifier: Modifier = Modifier,
    placeholder: String
) {
    val interactionSource = remember { MutableInteractionSource() }

    BasicTextField(
        value = value,
        onValueChange = onValueChange,
        singleLine = true,
        textStyle = LocalTextStyle.current.copy(fontSize = 13.sp),
        modifier = modifier
            .height(32.dp),
    ) { innerTextField ->
        OutlinedTextFieldDefaults.DecorationBox(
            value = value,
            innerTextField = innerTextField,
            enabled = true,
            singleLine = true,
            visualTransformation = VisualTransformation.None,
            interactionSource = interactionSource,
            placeholder = {
                Text(placeholder, fontSize = 12.sp)
            },
            contentPadding = OutlinedTextFieldDefaults.contentPadding(
                start = 8.dp,
                top = 4.dp,
                end = 8.dp,
                bottom = 4.dp
            ),
            colors = OutlinedTextFieldDefaults.colors(),
            container = {
                OutlinedTextFieldDefaults.Container(
                    enabled = true,
                    isError = false,
                    interactionSource = interactionSource,
                    colors = OutlinedTextFieldDefaults.colors()
                )
            }
        )
    }
}
