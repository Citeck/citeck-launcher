package ru.citeck.launcher.view.logs

import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import ru.citeck.launcher.view.utils.LogsUtils
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.atomic.AtomicLong
import kotlin.math.min

enum class LogLevel { ERROR, WARN, INFO, DEBUG, TRACE, UNKNOWN }

object LogLevelDetector {

    // Compiled regex patterns for log level detection (ordered by confidence)
    private val logLevelRegexPatterns = listOf(
        // Bracketed format: [ERROR], [WARN], [INFO], etc.
        Regex("""\[(ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE)]""", RegexOption.IGNORE_CASE),

        // Logback internal format: |-WARN, |-ERROR, |-INFO
        Regex("""\|-(ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE)\b""", RegexOption.IGNORE_CASE),

        // After timestamp with space: "10:30:45.123 ERROR" or "10:30:45 ERROR"
        Regex("""\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\s+(ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE)\b""", RegexOption.IGNORE_CASE),

        // Spring Boot format: "2024-01-15T10:30:45.123+00:00  INFO 12345"
        Regex("""\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[^\s]*\s+(ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE)\s""", RegexOption.IGNORE_CASE),

        // Python logging format: "ERROR:", "WARNING:", "INFO:"
        Regex("""^(ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE):""", RegexOption.IGNORE_CASE),

        // Level surrounded by whitespace
        Regex("""\s(ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE)\s""", RegexOption.IGNORE_CASE),

        // Level at line start followed by space/separator
        Regex("""^(ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE)[\s:\-\[]""", RegexOption.IGNORE_CASE)
    )

    fun detect(message: String): LogLevel {
        for (pattern in logLevelRegexPatterns) {
            val match = pattern.find(message)
            if (match != null) {
                val levelStr = match.groupValues[1].uppercase()
                return when (levelStr) {
                    "ERROR" -> LogLevel.ERROR
                    "WARN", "WARNING" -> LogLevel.WARN
                    "INFO" -> LogLevel.INFO
                    "DEBUG" -> LogLevel.DEBUG
                    "TRACE" -> LogLevel.TRACE
                    else -> continue
                }
            }
        }
        return LogLevel.UNKNOWN
    }
}

/**
 * Build list of messages with their log levels.
 * For lines without explicit level (like stack traces), inherit level from preceding line.
 */
internal fun buildMessagesWithLevels(state: LogsState.LogsList): List<Pair<String, LogLevel>> {
    if (state.size == 0) {
        return emptyList()
    }

    val result = ArrayList<Pair<String, LogLevel>>(state.size)
    var lastKnownLevel = LogLevel.UNKNOWN

    for (i in 0 until state.size) {
        val msg = state.messages[i].msg
        val detectedLevel = LogLevelDetector.detect(msg)

        val level = if (detectedLevel != LogLevel.UNKNOWN) {
            lastKnownLevel = detectedLevel
            detectedLevel
        } else {
            // Inherit level from previous line (for stack traces, etc.)
            lastKnownLevel
        }

        result.add(msg to level)
    }

    return result
}

class LogsState(
    messages: List<String> = emptyList(),
    val limit: Int
) {
    private val msgIdCounter = AtomicLong()

    private val messagesArray0 = LogsList(limit)
    private val messagesArray1 = LogsList(limit)

    @Volatile
    private var firstArrActive = true

    internal val messagesState: MutableState<LogsList> = mutableStateOf(messagesArray0)

    val totalMessages = mutableLongStateOf(0L)

    private val messagesQueue = ArrayBlockingQueue<LogMessage>(10000)

    init {
        messages.forEach { addMsg(it) }
    }

    fun clear() {
        messagesQueue.clear()
        messagesArray0.size = 0
        messagesArray1.size = 0
        totalMessages.longValue = 0L
        // Switch to the other array to trigger recomposition
        firstArrActive = !firstArrActive
        messagesState.value = if (firstArrActive) messagesArray0 else messagesArray1
    }

    fun getMessagesAsText(): String {
        val state = messagesState.value
        if (state.size == 0) return ""
        return buildString {
            for (i in 0 until state.size) {
                if (i > 0) append('\n')
                append(state.messages[i].msg)
            }
        }
    }

    internal fun consumeMessagesQueue(): Boolean {

        var queueMsg: LogMessage? = messagesQueue.poll() ?: return false
        val messagesToConsume = ArrayList<LogMessage>(min(messagesQueue.size + 100, limit))

        while (queueMsg != null) {
            messagesToConsume.add(queueMsg)
            if (messagesToConsume.size >= limit) {
                break
            }
            queueMsg = messagesQueue.poll()
        }

        val (activeArr, inactiveArr) = if (firstArrActive) {
            messagesArray0 to messagesArray1
        } else {
            messagesArray1 to messagesArray0
        }
        val currentSize = activeArr.size
        if ((currentSize + messagesToConsume.size) <= limit) {
            System.arraycopy(
                activeArr.messages,
                0,
                inactiveArr.messages,
                0,
                currentSize
            )
            messagesToConsume.forEachIndexed { idx, msg ->
                inactiveArr.messages[currentSize + idx] = msg
            }
            inactiveArr.size = currentSize + messagesToConsume.size
        } else {
            System.arraycopy(
                activeArr.messages,
                messagesToConsume.size,
                inactiveArr.messages,
                0,
                limit - messagesToConsume.size
            )
            messagesToConsume.forEachIndexed { idx, msg ->
                inactiveArr.messages[limit - (messagesToConsume.size - idx)] = msg
            }
            inactiveArr.size = limit
        }
        messagesState.value = inactiveArr
        firstArrActive = !firstArrActive
        totalMessages.value += messagesToConsume.size

        return true
    }

    fun addMsg(message: String) {

        if (message.contains('\n')) {
            message.split('\n').forEach { addMsg(it) }
            return
        }
        val fixedMessage = LogsUtils.normalizeMessage(message)

        messagesQueue.add(LogMessage(msgIdCounter.getAndIncrement(), fixedMessage))
    }

    internal class LogMessage(val id: Long, val msg: String)

    internal class LogsList(limit: Int) {
        val messages = Array(limit) { LogMessage(-1L, "") }

        @Volatile
        var size = 0
    }
}
