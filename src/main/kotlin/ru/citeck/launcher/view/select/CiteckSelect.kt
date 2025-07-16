package ru.citeck.launcher.view.select

import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.RemoveCircleOutline
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import ru.citeck.launcher.view.action.ActionDesc

@Composable
fun CiteckSelect(state: CiteckSelectState, mandatory: Boolean, onSelected: (String) -> Unit) {

    val expanded = remember { mutableStateOf(false) }
    val selectedValue = state.selected.value

    Box(
        modifier = Modifier.height(30.dp).border(1.dp, Color.Gray).clickable {
            val options = state.options.value
            if (options.size == 1 && options.first().button && options.first().value == state.selected.value) {
                onSelected(state.selected.value)
            } else {
                expanded.value = true
            }
        }
    ) {
        Text(
            state.options.value.find { it.value == selectedValue }?.name ?: selectedValue,
            style = LocalTextStyle.current.merge(MaterialTheme.typography.bodyMedium),
            modifier = Modifier.align(Alignment.CenterStart).padding(start = 16.dp).fillMaxWidth()
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
            contentDescription = "Select namespace"
        )

        DropdownMenu(expanded = expanded.value, onDismissRequest = { expanded.value = false }) {
            state.options.value.forEach { option ->
                DropdownMenuItem(
                    text = { Text(option.name) },
                    modifier = Modifier.height(30.dp),
                    onClick = {
                        expanded.value = false
                        if (state.selected.value != option.value) {
                            if (!option.button) {
                                state.selected.value = option.value
                            }
                            onSelected(option.value)
                        }
                    }
                )
            }
        }
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
