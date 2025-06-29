package ru.citeck.launcher.view.image

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.Density
import org.jetbrains.compose.resources.decodeToSvgPainter
import ru.citeck.launcher.core.utils.file.CiteckFiles
import java.util.concurrent.ConcurrentHashMap

object Icons {

    private val icons = ConcurrentHashMap<String, Painter>()

    private lateinit var density: Density

/*    @Composable
    fun Render() {
        remember(LocalDensity.current) {
            this.density = LocalDensity.current
            icons.clear()
        }
    }*/

    fun getPainter(icon: String): Painter {
        val iconToLoad = if (icon.contains(".")) icon else "$icon.svg"
        return icons.computeIfAbsent(iconToLoad) { iconKey ->
            val bytes = CiteckFiles.getFile("classpath:icons/$iconKey").read { it.readBytes() }
            if (iconKey.endsWith(".svg")) {
                bytes.decodeToSvgPainter(density)
            } else {
                error("Unsupported file type: $iconKey")
            }
        }
    }
}
