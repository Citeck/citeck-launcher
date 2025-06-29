package ru.citeck.launcher.view.logs

import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.platform.Font
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import kotlinx.coroutines.delay
import ru.citeck.launcher.view.utils.LogsUtils
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.atomic.AtomicLong
import kotlin.math.min

@Composable
fun LogsViewer(logsState: LogsState) {

    val listState = rememberLazyListState()
    val horizontalScrollState = rememberScrollState()
    val followLogs = remember { mutableStateOf(true) }
    val pause = remember { mutableStateOf(false) }
    val logsFont = remember {
        FontFamily(
            Font(
                resource = "fonts/ubuntu/UbuntuMono-R.ttf",
                weight = FontWeight.Normal,
                style = FontStyle.Normal
            )
        )
    }

    LaunchedEffect(logsState.totalMessages.value) {
        if (followLogs.value && !pause.value) {
            val lastItemIdx = logsState.messagesState.value.size - 1
            if (lastItemIdx > 0) {
                listState.scrollToItem(lastItemIdx)
            }
        }
    }
    LaunchedEffect("consume-log-messages") {
        while (true) {
            if (!logsState.consumeMessagesQueue()) {
                delay(500)
            }
        }
    }

    val messagesSnapshot = remember { mutableStateOf(emptyArray<LogsState.LogMessage>()) }
    Scaffold(
        bottomBar = {
            Column {
                HorizontalDivider(thickness = 1.dp, color = Color.Gray)
                Surface(
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(25.dp),
                    color = Color.White
                ) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 16.dp),
                        horizontalArrangement = Arrangement.End,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text("Pause")
                        Checkbox(
                            checked = pause.value,
                            onCheckedChange = {
                                if (it) {
                                    messagesSnapshot.value = logsState.getActiveMessagesSnapshot()
                                } else {
                                    messagesSnapshot.value = emptyArray()
                                }
                                pause.value = it
                            }
                        )
                        Text("Follow")
                        Checkbox(
                            checked = followLogs.value,
                            onCheckedChange = { followLogs.value = it }
                        )
                    }
                }
            }
        }
    ) { paddingValues ->
        Box(modifier = Modifier.padding(paddingValues)) {
            SelectionContainer {
                LazyColumn(
                    state = listState,
                    modifier = Modifier.fillMaxWidth().horizontalScroll(horizontalScrollState)
                ) {
                    val (messages, messagesSize) = if (pause.value) {
                        val messages = messagesSnapshot.value
                        messages to messages.size
                    } else {
                        val state = logsState.messagesState.value
                        state.messages to state.size
                    }
                    items(
                        messagesSize,
                        key = { messages[it].id }
                    ) { logMessageIdx ->
                        Text(
                            text = messages[logMessageIdx].msg,
                            softWrap = false,
                            fontFamily = logsFont,
                            fontSize = 1.em,
                            lineHeight = 0.em,
                            maxLines = 1
                        )
                    }
                }
            }
            VerticalScrollbar(
                adapter = rememberScrollbarAdapter(listState),
                modifier = Modifier.align(Alignment.CenterEnd).width(10.dp)
            )
        }
    }
}

class LogsState(
    messages: List<String> = emptyList(),
    private val limit: Int
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

    internal fun getActiveMessagesSnapshot(): Array<LogMessage> {
        val activeMessages = if (firstArrActive) messagesArray0 else messagesArray1
        return Array(activeMessages.size) { activeMessages.messages[it] }
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
