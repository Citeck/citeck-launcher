package ru.citeck.launcher.view.logs

import androidx.compose.foundation.HorizontalScrollbar
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.VerticalScrollbar
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollbarAdapter
import androidx.compose.foundation.text.BasicText
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.WrapText
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material3.Icon
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import ru.citeck.launcher.view.commons.CiteckTooltipArea

private const val SCROLL_THRESHOLD = 50

@Composable
fun LogsContent(
    annotatedText: AnnotatedString,
    textStyle: TextStyle,
    wordWrap: Boolean,
    verticalScrollState: ScrollState,
    horizontalScrollState: ScrollState,
    onTextLayout: (TextLayoutResult) -> Unit,
    onScrollUp: () -> Unit = {},
    onScrollToBottom: () -> Unit = {},
    modifier: Modifier = Modifier
) {
    // Detect user scrolling
    val previousScrollValue = remember { mutableIntStateOf(verticalScrollState.value) }
    LaunchedEffect(verticalScrollState.value, verticalScrollState.maxValue) {
        val delta = verticalScrollState.value - previousScrollValue.intValue
        val isAtBottom = verticalScrollState.maxValue - verticalScrollState.value < SCROLL_THRESHOLD

        when {
            // User scrolled up
            delta < -10 -> onScrollUp()
            // User reached bottom
            isAtBottom && delta > 0 -> onScrollToBottom()
        }
        previousScrollValue.intValue = verticalScrollState.value
    }

    Box(modifier = modifier) {
        SelectionContainer {
            BasicText(
                text = annotatedText,
                style = textStyle,
                onTextLayout = onTextLayout,
                softWrap = wordWrap,
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(verticalScrollState)
                    .then(
                        if (wordWrap) Modifier else Modifier.horizontalScroll(horizontalScrollState)
                    )
                    .padding(4.dp)
            )
        }

        VerticalScrollbar(
            adapter = rememberScrollbarAdapter(verticalScrollState),
            modifier = Modifier.align(Alignment.CenterEnd).width(10.dp)
        )
        if (!wordWrap) {
            HorizontalScrollbar(
                adapter = rememberScrollbarAdapter(horizontalScrollState),
                modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(10.dp)
            )
        }
    }
}

@Composable
fun LogsStatusBar(
    lineCount: Int,
    limit: Int,
    wordWrap: Boolean,
    followLogs: Boolean,
    onWordWrapChange: (Boolean) -> Unit,
    onScrollToBottom: () -> Unit
) {
    Surface(
        modifier = Modifier
            .fillMaxWidth()
            .height(28.dp),
        color = Color.White
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 12.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            // Left: Line count
            Text(
                "Lines: $lineCount / $limit",
                fontSize = 0.8.em,
                color = Color.Gray
            )

            // Right: Controls
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp)
            ) {
                WrapToggleButton(
                    checked = wordWrap,
                    onCheckedChange = onWordWrapChange
                )

                ScrollToBottomButton(
                    isActive = followLogs,
                    onClick = onScrollToBottom
                )
            }
        }
    }
}

@Composable
fun ScrollToBottomButton(
    isActive: Boolean,
    onClick: () -> Unit
) {
    CiteckTooltipArea(tooltip = "Follow logs") {
        Icon(
            imageVector = Icons.Default.KeyboardArrowDown,
            contentDescription = "Follow logs",
            modifier = Modifier
                .size(20.dp)
                .clickable { onClick() },
            tint = if (isActive) Color(0xFF1976D2) else Color.Gray
        )
    }
}

@Composable
fun WrapToggleButton(
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    CiteckTooltipArea(tooltip = "Word wrap") {
        Icon(
            imageVector = Icons.AutoMirrored.Filled.WrapText,
            contentDescription = "Word wrap",
            modifier = Modifier
                .size(20.dp)
                .clickable { onCheckedChange(!checked) },
            tint = if (checked) Color(0xFF1976D2) else Color.Gray
        )
    }
}

object LogSearch {

    fun findAllMatches(text: String, query: String): List<IntRange> {
        if (query.isEmpty()) return emptyList()
        val positions = mutableListOf<IntRange>()
        val lowerText = text.lowercase()
        val lowerQuery = query.lowercase()
        var index = 0
        while (index < lowerText.length) {
            val found = lowerText.indexOf(lowerQuery, index)
            if (found == -1) break
            positions.add(found until (found + query.length))
            index = found + 1
        }
        return positions
    }

    fun findAllMatchesRegex(text: String, pattern: String): List<IntRange> {
        return try {
            Regex(pattern, RegexOption.IGNORE_CASE)
                .findAll(text)
                .map { it.range }
                .toList()
        } catch (e: Exception) {
            emptyList()
        }
    }
}

object LogsTextBuilder {

    private val logLevelHighlightPatterns = mapOf(
        LogLevel.ERROR to Regex("""\[ERROR]|\|-ERROR\b|\bERROR\b""", RegexOption.IGNORE_CASE),
        LogLevel.WARN to Regex("""\[WARN(?:ING)?]|\|-WARN(?:ING)?\b|\bWARN(?:ING)?\b""", RegexOption.IGNORE_CASE),
        LogLevel.DEBUG to Regex("""\[DEBUG]|\|-DEBUG\b|\bDEBUG\b""", RegexOption.IGNORE_CASE),
        LogLevel.TRACE to Regex("""\[TRACE]|\|-TRACE\b|\bTRACE\b""", RegexOption.IGNORE_CASE),
        LogLevel.INFO to Regex("""\[INFO]|\|-INFO\b|\bINFO\b""", RegexOption.IGNORE_CASE)
    )

    fun buildColoredAndHighlightedText(
        linesWithLevels: List<Pair<String, LogLevel>>,
        matchPositions: List<IntRange>,
        currentMatchIndex: Int,
        highlightStyle: SpanStyle,
        currentHighlightStyle: SpanStyle,
        levelTextColors: Map<LogLevel, Color>
    ): AnnotatedString {
        return buildAnnotatedString {
            var currentPosition = 0
            var matchIdx = 0

            linesWithLevels.forEachIndexed { lineIndex, (line, level) ->
                if (lineIndex > 0) {
                    append('\n')
                    currentPosition++
                }

                val lineStart = currentPosition
                val lineEnd = currentPosition + line.length

                // Find the range of log level text to colorize
                val levelRange = findLogLevelRange(line, level)
                val levelColor = levelTextColors[level] ?: Color.Black

                // Process line character by character, applying appropriate styles
                var linePos = 0
                while (linePos < line.length) {
                    val globalPos = lineStart + linePos

                    // Check if there's a search match at this position
                    val matchingRange = matchPositions.getOrNull(matchIdx)
                    if (matchingRange != null && matchingRange.first <= globalPos && globalPos <= matchingRange.last) {
                        // Inside a search match
                        val matchEndInLine = minOf(matchingRange.last + 1 - lineStart, line.length)
                        val matchStartInLine = maxOf(matchingRange.first - lineStart, linePos)

                        // Append non-match text before
                        if (matchStartInLine > linePos) {
                            appendTextWithLevelHighlight(line, linePos, matchStartInLine, levelRange, levelColor)
                        }

                        // Append search match text
                        val style = if (matchIdx == currentMatchIndex) currentHighlightStyle else highlightStyle
                        withStyle(style) {
                            append(line.substring(matchStartInLine, matchEndInLine))
                        }

                        linePos = matchEndInLine

                        // Move to next match if we've passed this one
                        if (lineStart + linePos > matchingRange.last) {
                            matchIdx++
                        }
                    } else if (matchingRange != null && matchingRange.first < lineEnd && matchingRange.first >= globalPos) {
                        // Match starts later in this line
                        val matchStartInLine = matchingRange.first - lineStart
                        appendTextWithLevelHighlight(line, linePos, matchStartInLine, levelRange, levelColor)
                        linePos = matchStartInLine
                    } else {
                        // No more search matches in this line
                        appendTextWithLevelHighlight(line, linePos, line.length, levelRange, levelColor)
                        break
                    }
                }

                currentPosition = lineEnd
            }
        }
    }

    private fun findLogLevelRange(line: String, level: LogLevel): IntRange? {
        val pattern = logLevelHighlightPatterns[level] ?: return null
        return pattern.find(line)?.range
    }

    private fun AnnotatedString.Builder.appendTextWithLevelHighlight(
        line: String,
        start: Int,
        end: Int,
        levelRange: IntRange?,
        levelColor: Color
    ) {
        if (levelRange == null || end <= levelRange.first || start > levelRange.last) {
            // No overlap with level range - append as plain text
            append(line.substring(start, end))
        } else {
            // There's overlap with level range
            // Before level range
            if (start < levelRange.first) {
                append(line.substring(start, levelRange.first))
            }
            // Level range portion
            val levelStart = maxOf(start, levelRange.first)
            val levelEnd = minOf(levelRange.last + 1, end)
            if (levelStart < levelEnd) {
                withStyle(SpanStyle(color = levelColor, fontWeight = FontWeight.Bold)) {
                    append(line.substring(levelStart, levelEnd))
                }
            }
            // After level range
            if (end > levelRange.last + 1) {
                append(line.substring(levelRange.last + 1, end))
            }
        }
    }
}
