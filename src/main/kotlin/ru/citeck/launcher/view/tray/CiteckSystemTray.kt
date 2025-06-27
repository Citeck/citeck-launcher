package ru.citeck.launcher.view.tray

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.utils.CiteckEnvUtils
import ru.citeck.launcher.view.tray.gtk.*
import ru.citeck.launcher.view.utils.ImageUtils
import java.io.FileOutputStream
import java.nio.file.Path
import java.util.concurrent.CopyOnWriteArraySet
import kotlin.io.path.exists

object CiteckSystemTray {

    private val log = KotlinLogging.logger {}

    private val lmbClickListeners = CopyOnWriteArraySet<() -> Unit>()

    fun initialize(): Boolean {
        if (CiteckEnvUtils.isOsWindows()) {
            log.warn { "Windows tray is not supported yet" }
            return false
        }
        try {
            GtkTrayIndicator.loadLibraries()
        } catch (e: Throwable) {
            log.warn { "Gtk libraries loading failed" }
            return false
        }
        try {
            GtkTrayIndicator.create(initIcon().toString()) {
                lmbClickListeners.forEach { it.invoke() }
            }
        } catch (e: Throwable) {
            log.error(e) { "GtkInit error" }
            return false
        }
        return true
    }

    private fun initIcon(): Path {
        val pngData = ImageUtils.loadPng("classpath:logo.svg", 24f)

        val iconsPath = AppDir.PATH.resolve("icons")
        if (!iconsPath.exists()) {
            iconsPath.toFile().mkdir()
        }

        val fsIconPath = iconsPath.resolve("tray.png")

        val fsIconFile = fsIconPath.toFile()
        if (fsIconFile.exists() && fsIconFile.readBytes().contentEquals(pngData)) {
            return fsIconPath.toAbsolutePath()
        }

        FileOutputStream(fsIconPath.toFile()).use { it.write(pngData) }
        fsIconPath.toFile().setReadable(true, false)

        return fsIconPath.toAbsolutePath()
    }

    fun listenLmbClick(action: () -> Unit) {
        lmbClickListeners.add(action)
    }
}
