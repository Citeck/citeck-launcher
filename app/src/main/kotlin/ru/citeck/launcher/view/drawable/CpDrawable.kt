package ru.citeck.launcher.view.drawable

import androidx.compose.foundation.Image
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.DefaultAlpha
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalDensity
import org.jetbrains.compose.resources.decodeToSvgPainter
import ru.citeck.launcher.core.utils.file.CiteckFiles
import java.util.concurrent.ConcurrentHashMap
import kotlin.error
import kotlin.io.readBytes
import kotlin.text.endsWith

@Composable
fun CpImage(
    path: String,
    contentDescription: String? = null,
    modifier: Modifier = Modifier,
    alignment: Alignment = Alignment.Center,
    contentScale: ContentScale = ContentScale.Fit,
    alpha: Float = DefaultAlpha,
    colorFilter: ColorFilter? = null
) {
    Image(
        rememberCpPainter(path),
        contentDescription,
        modifier,
        alignment,
        contentScale,
        alpha,
        colorFilter
    )
}

@Composable
fun CpIcon(
    path: String,
    contentDescription: String? = null,
    modifier: Modifier = Modifier,
    tint: Color = LocalContentColor.current
) {
    Icon(
        rememberCpPainter(path),
        contentDescription,
        modifier,
        tint
    )
}

private val cpContentCache = ConcurrentHashMap<String, ByteArray>()

@Composable
fun rememberCpPainter(path: String): Painter {
    val density = LocalDensity.current
    return remember(path, density) {
        val bytes = cpContentCache.computeIfAbsent(path) { key ->
            CiteckFiles.getFile("classpath:$key").read { it.readBytes() }
        }
        if (path.endsWith(".svg")) {
            bytes.decodeToSvgPainter(density)
        } else {
            error("Unsupported file type: $path")
        }
    }
}
