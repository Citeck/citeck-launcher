package ru.citeck.launcher.view.select

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.RemoveCircleOutline
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInWindow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import ru.citeck.launcher.view.action.ActionDesc
import ru.citeck.launcher.view.commons.PopupInWindow

private const val ITEM_HEIGHT = 30

@Composable
fun CiteckSelect(
    state: CiteckSelectState,
    modifier: Modifier = Modifier,
    mandatory: Boolean,
    onSelected: (String) -> Unit
) {

    val expanded = remember { mutableStateOf(false) }
    val selectedValue = state.selected.value
    val selectedValueBounds = remember { mutableStateOf<LayoutCoordinates?>(null) }

    Box(
        modifier = modifier.height(ITEM_HEIGHT.dp)
            .border(1.dp, Color.Gray)
            .onGloballyPositioned { selectedValueBounds.value = it }
            .clickable {
                val options = state.options.value
                if (options.size == 1 && options.first().button && options.first().value == state.selected.value) {
                    onSelected(state.selected.value)
                } else {
                    if (options.size > 1 || options.size == 1 && options.first().value != state.selected.value) {
                        expanded.value = true
                    }
                }
            }
    ) {
        Text(
            state.options.value.find { it.value == selectedValue }?.name ?: selectedValue,
            modifier = Modifier.align(Alignment.CenterStart).padding(start = 10.dp).fillMaxWidth()
        )
        if (!mandatory && selectedValue.isNotBlank()) {
            Icon(
                Icons.Default.RemoveCircleOutline,
                modifier = Modifier.align(Alignment.CenterEnd)
                    .padding(end = 30.dp)
                    .requiredSize(20.dp).clickable { onSelected("") },
                contentDescription = "Delete"
            )
        }
        Icon(
            Icons.Default.ArrowDropDown,
            modifier = Modifier.align(Alignment.CenterEnd).padding(end = 5.dp).requiredSize(30.dp),
            contentDescription = "Select"
        )
        if (expanded.value) {
            val popupPosition = selectedValueBounds.value?.let {
                val popupPos = it.positionInWindow()
                IntOffset(popupPos.x.toInt(), (popupPos.y + it.size.height - 1).toInt())
            } ?: IntOffset.Zero

            val itemsValue = state.options.value
            val maxHeight = remember(selectedValueBounds.value) {
                ((selectedValueBounds.value?.size?.height ?: ITEM_HEIGHT) * 8).dp
            }

            PopupInWindow(
                offset = popupPosition,
                onDismissRequest = { expanded.value = false }
            ) {
                val itemsColumnLayout = remember { mutableStateOf<LayoutCoordinates?>(null) }
                val scrollState = rememberScrollState()

                Column(
                    modifier = Modifier
                        .background(MaterialTheme.colorScheme.surface)
                        .widthIn(min = 150.dp)
                        .heightIn(max = maxHeight)
                        .verticalScroll(scrollState)
                        .border(1.dp, Color.Gray)
                        .onGloballyPositioned { itemsColumnLayout.value = it }
                ) {
                    val minWidth = selectedValueBounds.value?.size?.width?.dp ?: itemsColumnLayout.value?.size?.width?.dp ?: 50.dp
                    for (option in itemsValue) {
                        if (option.value != state.selected.value) {
                            renderSelectPopupItem(minWidth, expanded, state, option, onSelected)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private inline fun renderSelectPopupItem(
    minWidth: Dp,
    expanded: MutableState<Boolean>,
    state: CiteckSelectState,
    option: SelectOption,
    crossinline onSelected: (String) -> Unit
) {
    Box(
        modifier = Modifier
            .wrapContentSize()
            .height(ITEM_HEIGHT.dp)
            .widthIn(min = minWidth)
            .clickable {
                expanded.value = false
                if (state.selected.value != option.value) {
                    if (!option.button) {
                        state.selected.value = option.value
                    }
                    onSelected(option.value)
                }
            }
    ) {
        Text(
            text = option.name,
            modifier = Modifier.align(Alignment.CenterStart).padding(start = 10.dp, end = 10.dp)
        )
        Box(modifier = Modifier.height(1.dp).background(Color.LightGray).widthIn(min = minWidth))
    }
}

class SelectOption(
    val name: String,
    val value: String = name,
    val button: Boolean = false,
    val actions: List<ActionDesc<String>> = emptyList(),
)

class CiteckSelectState(
    val options: MutableState<List<SelectOption>>,
    val selected: MutableState<String>
) {
    constructor(options: List<SelectOption>, selected: String) : this(
        mutableStateOf(options),
        mutableStateOf(selected)
    )
}
