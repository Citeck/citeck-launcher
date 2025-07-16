package ru.citeck.launcher.core.utils

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.utils.promise.Promise
import java.time.Duration
import java.util.concurrent.atomic.AtomicBoolean

object LongTaskUtils {

    private const val CHECK_INTERVAL = 1000L
    val PRINT_MSG_DEFAULT_INTERVAL: Duration = Duration.ofSeconds(10)
    val PRINT_STACKTRACE_DEFAULT_INTERVAL: Duration = Duration.ofSeconds(30)

    val log = KotlinLogging.logger {}

    fun <T> doWithWatcher(
        printMsgInterval: Duration = PRINT_MSG_DEFAULT_INTERVAL,
        printStackTraceInterval: Duration = PRINT_STACKTRACE_DEFAULT_INTERVAL,
        actionName: String,
        action: () -> T
    ): T {
        val actionThread = Thread.currentThread()
        val actionInProgress = AtomicBoolean(true)
        val watcherThread = Thread.ofVirtual().name("long-task-watcher-" + IdUtils.createStrId()).start {
            val startedAt = System.currentTimeMillis()
            var nextReportLogTime = System.currentTimeMillis() + printMsgInterval.toMillis()
            var nextReportThreadTime = System.currentTimeMillis() + printStackTraceInterval.toMillis()
            while (actionInProgress.get()) {
                val currentTime = System.currentTimeMillis()
                if (currentTime > nextReportLogTime) {
                    var message = "Action '$actionName' still in progress. Elapsed time: " + (currentTime - startedAt) + "ms"
                    if (currentTime > nextReportThreadTime) {
                        message += ". Action thread trace: \n" +
                            actionThread.stackTrace.joinToString("\n") { "  $it" }
                        nextReportThreadTime = currentTime + printStackTraceInterval.toMillis()
                    }
                    log.warn { message }
                    nextReportLogTime = currentTime + printMsgInterval.toMillis()
                }
                try {
                    Thread.sleep(CHECK_INTERVAL)
                } catch (_: InterruptedException) {
                    break
                }
            }
        }
        var isResultPromise = false
        try {
            var res: Any? = action()
            if (res is Promise<*>) {
                isResultPromise = true
                res = res.finally {
                    actionInProgress.set(false)
                    watcherThread.interrupt()
                }
            }
            @Suppress("UNCHECKED_CAST")
            return res as T
        } finally {
            if (!isResultPromise) {
                actionInProgress.set(false)
                watcherThread.interrupt()
            }
        }
    }
}
