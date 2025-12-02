package ru.citeck.launcher.view.action

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.view.commons.CiteckTooltipArea
import ru.citeck.launcher.view.commons.dialog.ErrorDialog
import ru.citeck.launcher.view.drawable.CpIcon

@Composable
fun IconBtn(
    icon: ActionIcon,
    tooltip: String,
    enabled: Boolean = true,
    size: Dp? = null,
    action: suspend () -> Unit
) {
    CiteckTooltipArea(tooltip) {
        CpIcon(
            "icons/${icon.name.lowercase().replace("_", "-") + ".svg"}",
            tint = if (!enabled) {
                Color.LightGray
            } else {
                Color.Unspecified
            },
            modifier = Modifier.padding(0.dp).let {
                if (size != null) {
                    it.requiredSize(size)
                } else {
                    it.fillMaxHeight()
                }
            }.clickable(enabled) {
                Thread.ofPlatform().start {
                    runBlocking {
                        try {
                            action()
                        } catch (e: Throwable) {
                            ErrorDialog.show(e)
                        }
                    }
                }
            }
        )
    }
}
