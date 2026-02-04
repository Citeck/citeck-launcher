package ru.citeck.launcher.core.utils.file

import ru.citeck.launcher.core.utils.Digest
import java.nio.file.Path
import java.text.SimpleDateFormat
import java.time.Instant
import java.util.Date
import java.util.UUID
import kotlin.io.path.exists
import kotlin.io.path.inputStream

object FileUtils {

    private val NOT_ALLOWED_FILENAME_CHARS_REGEX = Regex("[^a-zA-Z0-9._-]")
    private val FORBIDDEN_NAMES = setOf(
        "CON", "PRN", "AUX", "NUL",
        "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
        "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"
    )

    fun createNameWithCurrentDateTime(): String {
        return SimpleDateFormat("yy-MM-dd_HH-mm").format(Date.from(Instant.now()))
    }

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

    fun getFileSha256(path: Path): String {
        if (!path.exists()) {
            return ""
        }
        val sha256 = Digest.sha256()
        val buffer = ByteArray(8 * 1024)

        path.inputStream().use { input ->
            var bytesRead = input.read(buffer)
            while (bytesRead != -1) {
                sha256.update(buffer, 0, bytesRead)
                bytesRead = input.read(buffer)
            }
        }
        return sha256.toHex()
    }
}
