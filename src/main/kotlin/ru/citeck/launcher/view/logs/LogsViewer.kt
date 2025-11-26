package ru.citeck.launcher.view.logs

import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Scaffold
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.awt.Toolkit
import java.awt.datatransfer.StringSelection
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import javax.swing.JFileChooser
import javax.swing.filechooser.FileNameExtensionFilter
import java.awt.event.KeyEvent as AwtKeyEvent

private fun mapToLatinKey(key: Key): Key {
    val nativeKeyCode = (key.keyCode and 0xFFFFFFFFL).toInt()

    // First check if it's already a standard AWT key code
    when (nativeKeyCode) {
        AwtKeyEvent.VK_A -> return Key.A
        AwtKeyEvent.VK_B -> return Key.B
        AwtKeyEvent.VK_C -> return Key.C
        AwtKeyEvent.VK_D -> return Key.D
        AwtKeyEvent.VK_E -> return Key.E
        AwtKeyEvent.VK_F -> return Key.F
        AwtKeyEvent.VK_G -> return Key.G
        AwtKeyEvent.VK_H -> return Key.H
        AwtKeyEvent.VK_I -> return Key.I
        AwtKeyEvent.VK_J -> return Key.J
        AwtKeyEvent.VK_K -> return Key.K
        AwtKeyEvent.VK_L -> return Key.L
        AwtKeyEvent.VK_M -> return Key.M
        AwtKeyEvent.VK_N -> return Key.N
        AwtKeyEvent.VK_O -> return Key.O
        AwtKeyEvent.VK_P -> return Key.P
        AwtKeyEvent.VK_Q -> return Key.Q
        AwtKeyEvent.VK_R -> return Key.R
        AwtKeyEvent.VK_S -> return Key.S
        AwtKeyEvent.VK_T -> return Key.T
        AwtKeyEvent.VK_U -> return Key.U
        AwtKeyEvent.VK_V -> return Key.V
        AwtKeyEvent.VK_W -> return Key.W
        AwtKeyEvent.VK_X -> return Key.X
        AwtKeyEvent.VK_Y -> return Key.Y
        AwtKeyEvent.VK_Z -> return Key.Z
    }

    // Check if it's a Unicode character (has 0x1000000 prefix on macOS)
    if (nativeKeyCode and 0x1000000 != 0) {
        val unicodeChar = nativeKeyCode and 0xFFFF

        // Map Russian characters to Latin based on QWERTY keyboard layout
        return when (unicodeChar.toChar()) {
            // Top row: Й Ц У К Е Н Г Ш Щ З Х Ъ -> Q W E R T Y U I O P [ ]
            'й', 'Й' -> Key.Q
            'ц', 'Ц' -> Key.W
            'у', 'У' -> Key.E
            'к', 'К' -> Key.R
            'е', 'Е' -> Key.T
            'н', 'Н' -> Key.Y
            'г', 'Г' -> Key.U
            'ш', 'Ш' -> Key.I
            'щ', 'Щ' -> Key.O
            'з', 'З' -> Key.P

            // Home row: Ф Ы В А П Р О Л Д Ж Э -> A S D F G H J K L ; '
            'ф', 'Ф' -> Key.A
            'ы', 'Ы' -> Key.S
            'в', 'В' -> Key.D
            'а', 'А' -> Key.F
            'п', 'П' -> Key.G
            'р', 'Р' -> Key.H
            'о', 'О' -> Key.J
            'л', 'Л' -> Key.K
            'д', 'Д' -> Key.L

            // Bottom row: Я Ч С М И Т Ь Б Ю -> Z X C V B N M , .
            'я', 'Я' -> Key.Z
            'ч', 'Ч' -> Key.X
            'с', 'С' -> Key.C
            'м', 'М' -> Key.V
            'и', 'И' -> Key.B
            'т', 'Т' -> Key.N
            'ь', 'Ь' -> Key.M

            else -> key
        }
    }

    return key
}

@Composable
fun LogsViewer(
    logsState: LogsState,
    windowTitle: String = "Logs",
    onClose: (() -> Unit)? = null
) {
    val verticalScrollState = rememberScrollState()
    val horizontalScrollState = rememberScrollState()
    val coroutineScope = rememberCoroutineScope()
    val followLogs = remember { mutableStateOf(true) }
    val searchQuery = remember { mutableStateOf("") }
    val searchVisible = remember { mutableStateOf(false) }
    val currentMatchIndex = remember { mutableIntStateOf(0) }
    val textLayoutResult = remember { mutableStateOf<TextLayoutResult?>(null) }
    val searchFieldFocusRequester = remember { FocusRequester() }
    val mainFocusRequester = remember { FocusRequester() }

    // Toolbar feature states
    val wordWrap = remember { mutableStateOf(false) }
    val useRegex = remember { mutableStateOf(false) }
    val copiedFeedback = remember { mutableStateOf(false) }

    val filterText = remember { mutableStateOf("") }

    val levelFilters = remember {
        mutableStateMapOf(
            LogLevel.ERROR to true,
            LogLevel.WARN to true,
            LogLevel.INFO to true,
            LogLevel.DEBUG to true,
            LogLevel.TRACE to true,
            LogLevel.UNKNOWN to true
        )
    }

    val logsTextStyle = rememberLogsTextStyle()

    // Toolbar action functions
    fun copyAllToClipboard() {
        val text = logsState.getMessagesAsText()
        Toolkit.getDefaultToolkit().systemClipboard.setContents(StringSelection(text), null)
        copiedFeedback.value = true
    }

    fun exportToFile() {
        val fileChooser = JFileChooser()
        val timestamp = SimpleDateFormat("yyyyMMdd_HHmmss").format(Date())
        val defaultFileName = "${windowTitle.replace(" ", "_")}_$timestamp.log"
        fileChooser.selectedFile = File(defaultFileName)
        fileChooser.fileFilter = FileNameExtensionFilter("Log files (*.log, *.txt)", "log", "txt")
        if (fileChooser.showSaveDialog(null) == JFileChooser.APPROVE_OPTION) {
            val file = fileChooser.selectedFile
            file.writeText(logsState.getMessagesAsText())
        }
    }

    // Reset copied feedback after delay
    LaunchedEffect(copiedFeedback.value) {
        if (copiedFeedback.value) {
            delay(2000)
            copiedFeedback.value = false
        }
    }

    fun goToNextMatch(matchCount: Int) {
        if (matchCount > 0) {
            currentMatchIndex.intValue = (currentMatchIndex.intValue + 1) % matchCount
        }
    }

    fun goToPreviousMatch(matchCount: Int) {
        if (matchCount > 0) {
            currentMatchIndex.intValue =
                (currentMatchIndex.intValue - 1 + matchCount) % matchCount
        }
    }

    // Auto-scroll to bottom when following
    LaunchedEffect(logsState.totalMessages.value) {
        if (followLogs.value && searchQuery.value.isEmpty()) {
            verticalScrollState.scrollTo(verticalScrollState.maxValue)
        }
    }

    LaunchedEffect("consume-log-messages") {
        while (true) {
            if (!logsState.consumeMessagesQueue()) {
                delay(500)
            }
        }
    }

    LaunchedEffect(searchVisible.value) {
        if (searchVisible.value) {
            searchFieldFocusRequester.requestFocus()
        } else {
            mainFocusRequester.requestFocus()
        }
    }

    LaunchedEffect(Unit) {
        mainFocusRequester.requestFocus()
        delay(300)
        verticalScrollState.scrollTo(verticalScrollState.maxValue)
    }

    // Convert wildcard pattern to regex once when filter changes
    val filterPattern = remember(filterText.value) {
        if (filterText.value.isEmpty() || filterText.value.length < 2) {
            null
        } else {
            try {
                // Convert wildcard pattern (* matches any characters) to regex
                val regexPattern = filterText.value
                    .replace("\\", "\\\\")
                    .replace(".", "\\.")
                    .replace("?", "\\?")
                    .replace("+", "\\+")
                    .replace("^", "\\^")
                    .replace("$", "\\$")
                    .replace("|", "\\|")
                    .replace("[", "\\[")
                    .replace("]", "\\]")
                    .replace("(", "\\(")
                    .replace(")", "\\)")
                    .replace("{", "\\{")
                    .replace("}", "\\}")
                    .replace("*", ".*")
                Regex(regexPattern, RegexOption.IGNORE_CASE)
            } catch (_: Exception) {
                null
            }
        }
    }

    // Get filtered logs with level information
    val filteredLogsData = remember(
        logsState.messagesState.value,
        levelFilters.toMap(),
        filterText.value,
        filterPattern
    ) {
        val messagesWithLevels: List<Pair<String, LogLevel>> = buildMessagesWithLevels(logsState.messagesState.value)

        messagesWithLevels.mapNotNull { (line, level) ->
            // First apply level filter
            if (levelFilters[level] == false) {
                return@mapNotNull null
            }

            // Then apply text filter with wildcard support
            if (filterText.value.length >= 2 && filterPattern != null) {
                val matches = filterPattern.containsMatchIn(line)
                // Show lines that match the filter
                if (!matches) {
                    return@mapNotNull null
                }
            }

            line to level
        }
    }

    val logsText = remember(filteredLogsData) {
        filteredLogsData.joinToString("\n") { it.first }
    }

    val matchPositions = remember(logsText, searchQuery.value, useRegex.value) {
        if (searchQuery.value.isEmpty() || searchQuery.value.length < 2) {
            emptyList()
        } else if (useRegex.value) {
            LogSearch.findAllMatchesRegex(logsText, searchQuery.value)
        } else {
            LogSearch.findAllMatches(logsText, searchQuery.value)
        }
    }

    LaunchedEffect(matchPositions, currentMatchIndex.intValue) {
        textLayoutResult.value?.let { layout ->
            if (matchPositions.isNotEmpty()) {
                val matchIndex = currentMatchIndex.intValue.coerceIn(0, matchPositions.size - 1)
                val matchPosition = matchPositions[matchIndex].first
                val lineIndex = layout.getLineForOffset(matchPosition)
                val lineTop = layout.getLineTop(lineIndex).toInt()
                verticalScrollState.animateScrollTo(lineTop)
            }
        }
    }

    val annotatedLogsText = remember(
        filteredLogsData,
        searchQuery.value,
        currentMatchIndex.intValue,
        matchPositions,
        LogLevelColors.textColors
    ) {
        LogsTextBuilder.buildColoredAndHighlightedText(
            filteredLogsData,
            matchPositions,
            currentMatchIndex.intValue,
            LogLevelColors.highlightStyle,
            LogLevelColors.currentHighlightStyle,
            LogLevelColors.textColors
        )
    }

    Scaffold(
        modifier = Modifier
            .focusRequester(mainFocusRequester)
            .focusable()
            .onKeyEvent { event ->
                if (event.type == KeyEventType.KeyDown) {
                    val hasModifier = event.isCtrlPressed || event.isMetaPressed

                    // Map Russian keys to their Latin equivalents based on physical keyboard position
                    val effectiveKey = mapToLatinKey(event.key)

                    when {
                        // Ctrl+F or Cmd+F to open search
                        effectiveKey == Key.F && hasModifier && !event.isShiftPressed -> {
                            searchVisible.value = true
                            true
                        }
                        // Escape to close search or close window
                        event.key == Key.Escape -> {
                            if (searchVisible.value) {
                                searchVisible.value = false
                                searchQuery.value = ""
                                currentMatchIndex.intValue = 0
                            } else {
                                onClose?.invoke()
                            }
                            true
                        }
                        // F3 or Ctrl+G for next match
                        (event.key == Key.F3 || (effectiveKey == Key.G && hasModifier)) &&
                            !event.isShiftPressed -> {
                            goToNextMatch(matchPositions.size)
                            true
                        }
                        // Shift+F3 or Ctrl+Shift+G for previous match
                        (event.key == Key.F3 || (effectiveKey == Key.G && hasModifier)) &&
                            event.isShiftPressed -> {
                            goToPreviousMatch(matchPositions.size)
                            true
                        }
                        // Ctrl+Shift+C to copy all
                        effectiveKey == Key.C && hasModifier && event.isShiftPressed -> {
                            copyAllToClipboard()
                            true
                        }
                        // Ctrl+L to clear logs
                        effectiveKey == Key.L && hasModifier -> {
                            logsState.clear()
                            true
                        }
                        // Ctrl+S to export
                        effectiveKey == Key.S && hasModifier -> {
                            exportToFile()
                            true
                        }

                        else -> false
                    }
                } else {
                    false
                }
            },
        topBar = {
            Column {
                // Toolbar row
                LogsToolbar(
                    filterText = filterText.value,
                    onFilterTextChange = { filterText.value = it },
                    levelFilters = levelFilters,
                    onCopy = { copyAllToClipboard() },
                    onClear = { logsState.clear() },
                    onExport = { exportToFile() },
                    copiedFeedback = copiedFeedback.value
                )

                // Search row (conditional)
                if (searchVisible.value) {
                    LogsSearchBar(
                        searchQuery = searchQuery.value,
                        onSearchQueryChange = {
                            searchQuery.value = it
                            currentMatchIndex.intValue = 0
                        },
                        useRegex = useRegex.value,
                        onUseRegexChange = {
                            useRegex.value = it
                            currentMatchIndex.intValue = 0
                        },
                        matchCount = matchPositions.size,
                        currentMatchIndex = currentMatchIndex.intValue,
                        onNextMatch = { goToNextMatch(matchPositions.size) },
                        onPreviousMatch = { goToPreviousMatch(matchPositions.size) },
                        onClose = {
                            searchVisible.value = false
                            searchQuery.value = ""
                            currentMatchIndex.intValue = 0
                        },
                        focusRequester = searchFieldFocusRequester
                    )
                }
                HorizontalDivider(thickness = 1.dp, color = Color.LightGray)
            }
        },
        bottomBar = {
            Column {
                HorizontalDivider(thickness = 1.dp, color = Color.Gray)
                LogsStatusBar(
                    lineCount = logsState.messagesState.value.size,
                    limit = logsState.limit,
                    wordWrap = wordWrap.value,
                    followLogs = followLogs.value,
                    onWordWrapChange = { wordWrap.value = it },
                    onScrollToBottom = {
                        followLogs.value = true
                        coroutineScope.launch {
                            verticalScrollState.scrollTo(verticalScrollState.maxValue)
                        }
                    }
                )
            }
        }
    ) { paddingValues ->
        Box(modifier = Modifier.padding(paddingValues)) {
            LogsContent(
                annotatedText = annotatedLogsText,
                textStyle = logsTextStyle,
                wordWrap = wordWrap.value,
                verticalScrollState = verticalScrollState,
                horizontalScrollState = horizontalScrollState,
                onTextLayout = { textLayoutResult.value = it },
                onScrollUp = { followLogs.value = false },
                onScrollToBottom = { followLogs.value = true }
            )
        }
    }
}
