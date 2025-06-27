package ru.citeck.launcher.view.image

import androidx.compose.foundation.Image
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.DefaultAlpha
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalDensity
import org.jetbrains.compose.resources.decodeToSvgPainter
import ru.citeck.launcher.core.utils.file.CiteckFiles

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
    val density = LocalDensity.current
    val painter = remember(path) {
        val bytes = CiteckFiles.getFile("classpath:$path").read { it.readBytes() }
        if (path.endsWith(".svg")) {
            bytes.decodeToSvgPainter(density)
        } else {
            error("Unsupported file type: $path")
        }
    }
    Image(
        painter,
        contentDescription,
        modifier,
        alignment,
        contentScale,
        alpha,
        colorFilter
    )
}
