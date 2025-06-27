package ru.citeck.launcher.core.logs

import org.apache.commons.io.input.Tailer
import org.apache.commons.io.input.TailerListenerAdapter
import ru.citeck.launcher.core.config.AppDir
import java.nio.file.Path

object AppLogUtils {

    fun getAppLogFilePath(): Path {
        return AppDir.PATH.resolve("logs/logfile.log")
    }

    fun watchAppLogs(action: (String) -> Unit): AutoCloseable {
        val tailer = Tailer.builder().setPath(getAppLogFilePath())
            .setReOpen(false)
            .setStartThread(false)
            .setTailerListener(object : TailerListenerAdapter() {
                override fun handle(line: String) {
                    action(line)
                }
            })
            .get()

        Thread.ofVirtual().start(tailer)
        return tailer
    }
}
