package ru.citeck.launcher.view.action

import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.material.Icon
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import ru.citeck.launcher.view.commons.dialog.ErrorDialog
import ru.citeck.launcher.view.drawable.CpIcon
import ru.citeck.launcher.view.table.CiteckIconButton

@Composable
fun CiteckIconAction(
    actionsCoroutineScope: CoroutineScope,
    modifier: Modifier = Modifier,
    actionDesc: ActionDesc<Unit>
) {
    CiteckIconAction(actionsCoroutineScope, true, modifier, actionDesc)
}

@Composable
fun CiteckIconAction(
    actionsCoroutineScope: CoroutineScope,
    enabled: Boolean = true,
    modifier: Modifier = Modifier,
    actionDesc: ActionDesc<Unit>
) {
    CiteckIconAction(actionsCoroutineScope, enabled, actionDesc, Unit, modifier = modifier)
}

@Composable
fun <T> CiteckIconAction(
    actionsCoroutineScope: CoroutineScope,
    actionDesc: ActionDesc<T>,
    actionParam: T,
    modifier: Modifier = Modifier,
    afterAction: (T) -> Unit = {}
) {
    CiteckIconAction(
        actionsCoroutineScope,
        true,
        actionDesc,
        actionParam,
        modifier,
        afterAction
    )
}

@Composable
fun <T> CiteckIconAction(
    actionsCoroutineScope: CoroutineScope,
    enabled: Boolean = true,
    actionDesc: ActionDesc<T>,
    actionParam: T,
    modifier: Modifier = Modifier,
    afterAction: (T) -> Unit = {}
) {
    CiteckIconButton(modifier = modifier, enabled = enabled, onClick = {
        actionsCoroutineScope.launch {
            ErrorDialog.doActionSafe(
                { actionDesc.action.invoke(actionParam) },
                { "Action '${actionDesc.id}' failed" },
                { afterAction.invoke(actionParam) }
            )
        }
    }) {
        renderIcon(actionDesc.icon, actionDesc.description, enabled)
    }
}

@Composable
private fun renderIcon(icon: ActionIcon, description: String, enabled: Boolean) {
    when (icon) {
        ActionIcon.DELETE -> renderIconFromClasspath("delete.svg", description, 20, enabled = enabled)
        ActionIcon.STOP -> renderIconFromClasspath("stop.svg", description, 20, enabled = enabled)
        ActionIcon.START -> renderIconFromClasspath("start.svg", description, 20, enabled = enabled)
        ActionIcon.PLUS -> renderIconFromClasspath("plus.svg", description, 20, enabled = enabled)
        ActionIcon.MINUS -> renderIconFromClasspath("minus.svg", description, 20, enabled = enabled)
        ActionIcon.EDIT -> renderDefaultIcon(Icons.Outlined.Edit, description)
        ActionIcon.DEPLOY -> renderIconFromClasspath("deploy.svg", description, enabled = enabled)
        ActionIcon.STOP_ALL -> renderIconFromClasspath("stop-all.svg", description, enabled = enabled)
        ActionIcon.RECREATE_NS -> renderIconFromClasspath("recreate.svg", description, enabled = enabled)
        ActionIcon.PORTS_ON -> renderIconFromClasspath("ports-on.svg", description, enabled = enabled)
        ActionIcon.PORTS_OFF -> renderIconFromClasspath("ports-off.svg", description, enabled = enabled)
        ActionIcon.DELETE_VOLUMES -> renderIconFromClasspath("delete-volumes.svg", description, enabled = enabled)
        ActionIcon.LOGS -> renderIconFromClasspath("logs.svg", description, enabled = enabled)
        ActionIcon.OPEN_DIR -> renderIconFromClasspath("open-dir.svg", description, enabled = enabled)
        ActionIcon.ARROW_LEFT -> renderIconFromClasspath("arrow-left.svg", description, enabled = enabled)
        ActionIcon.KEY -> renderIconFromClasspath("key.svg", description, enabled = enabled)
        ActionIcon.STORAGE -> renderIconFromClasspath("storage.svg", description, enabled = enabled)
        ActionIcon.EXCLAMATION_TRIANGLE -> renderIconFromClasspath("exclamation-triangle.svg", description, enabled = enabled)
        ActionIcon.COG_6_TOOTH -> renderIconFromClasspath("cog-6-tooth.svg", description, enabled = enabled)
        ActionIcon.ELLIPSIS_VERTICAL -> renderIconFromClasspath("ellipsis-vertical.svg", description, enabled = enabled)
        ActionIcon.BARS_ARROW_DOWN -> renderIconFromClasspath("bars-arrow-down.svg", description, enabled = enabled)
    }
}

@Composable
private fun renderDefaultIcon(image: ImageVector, description: String) {
    Icon(
        image,
        modifier = Modifier.padding(0.dp).requiredSize(30.dp),
        contentDescription = description
    )
}

@Composable
private fun renderIconFromClasspath(path: String, description: String, size: Int = 28, enabled: Boolean) {
    CpIcon(
        "icons/$path",
        tint = if (!enabled) {
            Color.Gray
        } else {
            Color.Unspecified
        },
        modifier = Modifier.padding(0.dp).requiredSize(size.dp),
    )
}
