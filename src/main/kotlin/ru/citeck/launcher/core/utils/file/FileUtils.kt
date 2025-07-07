package ru.citeck.launcher.core.utils.file

import java.util.UUID

object FileUtils {

    private val NOT_ALLOWED_FILENAME_CHARS_REGEX = Regex("[^a-zA-Z0-9._-]")
    private val FORBIDDEN_NAMES = setOf(
        "CON", "PRN", "AUX", "NUL",
        "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
        "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"
    )

    fun sanitizeFileName(name: String): String {

        var sanitized = name.trim()

        sanitized = sanitized.replace(NOT_ALLOWED_FILENAME_CHARS_REGEX, "_")
        sanitized = sanitized.trimEnd('.')

        val nameWithoutExtension = sanitized.substringBeforeLast('.', sanitized)
        val extension = sanitized.substringAfterLast('.', "")

        if (FORBIDDEN_NAMES.contains(nameWithoutExtension.uppercase())) {
            sanitized = "${nameWithoutExtension}_file" + if (extension.isNotEmpty()) ".$extension" else ""
        }
        if (sanitized.isBlank()) {
            sanitized = UUID.randomUUID().toString()
        }

        return sanitized
    }
}
