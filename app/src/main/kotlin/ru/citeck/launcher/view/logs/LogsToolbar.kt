package ru.citeck.launcher.view.logs

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Clear
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.SaveAlt
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.snapshots.SnapshotStateMap
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import ru.citeck.launcher.view.commons.CiteckTooltipArea

private val isMac = System.getProperty("os.name").lowercase().contains("mac")
private val modifierKey = if (isMac) "⌘" else "Ctrl"

@Composable
fun LogsToolbar(
    filterText: String,
    onFilterTextChange: (String) -> Unit,
    levelFilters: SnapshotStateMap<LogLevel, Boolean>,
    onCopy: () -> Unit,
    onClear: () -> Unit,
    onExport: () -> Unit,
    copiedFeedback: Boolean
) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        color = Color(0xFFF5F5F5)
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 8.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            // Left side: Text filter + Level filters
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp)
            ) {
                // Text filter
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(4.dp)
                ) {
                    CompactSearchField(
                        value = filterText,
                        placeholder = "Filter",
                        onValueChange = onFilterTextChange,
                        modifier = Modifier.width(300.dp),
                    )
                    if (filterText.isNotEmpty()) {
                        CiteckTooltipArea("Clear filter") {
                            IconButton(
                                onClick = { onFilterTextChange("") },
                                modifier = Modifier.size(20.dp)
                            ) {
                                Icon(
                                    Icons.Outlined.Clear,
                                    contentDescription = "Clear filter",
                                    modifier = Modifier.size(14.dp)
                                )
                            }
                        }
                    }
                }

                // Level filters
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    LogLevel.entries.filter { it != LogLevel.UNKNOWN }.forEach { level ->
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Checkbox(
                                checked = levelFilters[level] ?: true,
                                onCheckedChange = { levelFilters[level] = it },
                                modifier = Modifier.size(16.dp),
                                colors = CheckboxDefaults.colors(
                                    checkedColor = LogLevelColors.textColors[level] ?: Color.Black
                                )
                            )
                            Spacer(Modifier.width(4.dp))
                            Text(
                                level.name,
                                fontSize = 0.7.em,
                                color = LogLevelColors.textColors[level] ?: Color.Black
                            )
                        }
                    }
                }
            }

            // Right side: Action buttons
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp)
            ) {
                CiteckTooltipArea(
                    if (copiedFeedback) "Copied!" else "Copy all ($modifierKey+Shift+C)"
                ) {
                    Icon(
                        Icons.Outlined.ContentCopy,
                        contentDescription = "Copy all",
                        modifier = Modifier
                            .size(20.dp)
                            .clickable { onCopy() },
                        tint = if (copiedFeedback) Color(0xFF4CAF50) else Color.Gray
                    )
                }
                CiteckTooltipArea("Clear logs ($modifierKey+L)") {
                    Icon(
                        Icons.Outlined.Delete,
                        contentDescription = "Clear",
                        modifier = Modifier
                            .size(20.dp)
                            .clickable { onClear() },
                        tint = Color.Gray
                    )
                }
                CiteckTooltipArea("Export to file ($modifierKey+S)") {
                    Icon(
                        Icons.Outlined.SaveAlt,
                        contentDescription = "Export",
                        modifier = Modifier
                            .size(20.dp)
                            .clickable { onExport() },
                        tint = Color.Gray
                    )
                }
            }
        }
    }
}

@Composable
fun LogsSearchBar(
    searchQuery: String,
    onSearchQueryChange: (String) -> Unit,
    useRegex: Boolean,
    onUseRegexChange: (Boolean) -> Unit,
    matchCount: Int,
    currentMatchIndex: Int,
    onNextMatch: () -> Unit,
    onPreviousMatch: () -> Unit,
    onClose: () -> Unit,
    focusRequester: FocusRequester
) {
    Surface(
        modifier = Modifier.fillMaxWidth(),
        color = Color(0xFFEEEEEE)
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 8.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            CompactSearchField(
                value = searchQuery,
                placeholder = "Search",
                onValueChange = onSearchQueryChange,
                modifier = Modifier
                    .weight(1f)
                    .focusRequester(focusRequester)
                    .onKeyEvent { event ->
                        if (event.type == KeyEventType.KeyDown && event.key == Key.Enter) {
                            if (event.isShiftPressed) {
                                onPreviousMatch()
                            } else {
                                onNextMatch()
                            }
                            true
                        } else {
                            false
                        }
                    }
            )

            Spacer(modifier = Modifier.width(8.dp))

            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(
                    checked = useRegex,
                    onCheckedChange = onUseRegexChange,
                    modifier = Modifier.size(18.dp)
                )
                Spacer(Modifier.width(6.dp))
                Text(".*", fontSize = 14.sp, fontWeight = FontWeight.Bold)
            }

            Spacer(modifier = Modifier.width(8.dp))

            Text(
                text = when {
                    matchCount == 0 && searchQuery.length >= 2 -> "0/0"
                    matchCount > 0 -> "${currentMatchIndex + 1}/$matchCount"
                    else -> ""
                },
                modifier = Modifier.width(60.dp)
            )

            IconButton(
                onClick = onPreviousMatch,
                enabled = matchCount > 0
            ) {
                Text("\u25B2")
            }
            IconButton(
                onClick = onNextMatch,
                enabled = matchCount > 0
            ) {
                Text("\u25BC")
            }
            IconButton(
                onClick = onClose
            ) {
                Text("\u2715")
            }
        }
    }
}
