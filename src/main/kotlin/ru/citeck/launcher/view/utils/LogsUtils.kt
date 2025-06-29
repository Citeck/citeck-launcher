package ru.citeck.launcher.view.utils

object LogsUtils {

    fun normalizeMessage(message: String): String {
        return message.replace("\u001B\\[[\\d;]+m".toRegex(), "")
            .replace("\t", "    ")
    }
}
