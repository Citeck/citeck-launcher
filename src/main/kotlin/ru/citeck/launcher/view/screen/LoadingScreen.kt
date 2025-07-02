package ru.citeck.launcher.view.screen

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.delay
import ru.citeck.launcher.core.logs.AppLogUtils
import ru.citeck.launcher.view.logs.GlobalLogsWindow
import ru.citeck.launcher.view.logs.LogsDialogParams
import ru.citeck.launcher.view.utils.FeedbackUtils

private val log = KotlinLogging.logger { }

@Composable
fun LoadingScreen() {
    val longDelay = remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        delay(20_000)
        longDelay.value = true
        log.warn { "Loading takes too long" }
    }
    Box(modifier = Modifier.fillMaxSize()) {
        Column(modifier = Modifier.align(Alignment.Center)) {
            Text(
                text = "Loading...",
                fontSize = 2.em,
            )
            if (longDelay.value) {
                Spacer(modifier = Modifier.height(10.dp))
                Text(
                    text = "Still loading... This is taking longer than expected.\n" +
                        "To help us diagnose the issue, please click the \"Dump System Info\"\n" +
                        "button at the bottom and send the data to the maintainers.",
                )
            }
        }
        Row(modifier = Modifier.align(Alignment.BottomStart).height(40.dp).padding(10.dp)) {
            Text(
                text = "Show Logs",
                fontSize = 0.8.em,
                modifier = Modifier.clickable {
                    GlobalLogsWindow.show(
                        LogsDialogParams("Launcher Logs", 5000) { logsCallback ->
                            runCatching {
                                AppLogUtils.watchAppLogs { logsCallback.invoke(it) }
                            }
                        }
                    )
                }
            )
            Spacer(Modifier.width(5.dp))
            VerticalDivider()
            Spacer(Modifier.width(5.dp))
            Text(
                text = "Dump System Info",
                fontSize = 0.8.em,
                modifier = Modifier.clickable {
                    FeedbackUtils.dumpSystemInfo(true)
                }
            )
        }
    }
}
