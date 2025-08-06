package ru.citeck.launcher.view.tray

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.view.tray.gtk.*
import ru.citeck.launcher.view.utils.ImageUtils
import java.awt.*
import java.awt.event.MouseEvent
import java.awt.event.MouseListener
import java.io.FileOutputStream
import java.nio.file.Path
import java.util.concurrent.CopyOnWriteArraySet
import kotlin.io.path.exists
import kotlin.system.exitProcess

object CiteckSystemTray {

    const val BTN_OPEN = "Open"
    const val BTN_EXIT = "Exit"

    private val log = KotlinLogging.logger {}

    private val lmbClickListeners = CopyOnWriteArraySet<() -> Unit>()

    fun initialize(): Boolean {

        if (SystemTray.isSupported()) {
            return try {
                createDefaultTray()
                true
            } catch (e: Throwable) {
                log.error(e) { "System tray could not be initialized" }
                false
            }
        }
        try {
            GtkTrayIndicator.loadLibraries()
        } catch (e: Throwable) {
            log.warn { "Gtk libraries loading failed. Tray won't work." }
            return false
        }
        try {
            GtkTrayIndicator.create(initIcon().toString()) {
                lmbClickListeners.forEach { it.invoke() }
            }
        } catch (e: Throwable) {
            log.error(e) { "GtkInit error. Tray won't work" }
            return false
        }
        return true
    }

    private fun createDefaultTray() {

        // val isMac = CiteckEnvUtils.isOsMac()

        val systemTray = SystemTray.getSystemTray()
        val iconSize = 64f
        val iconBorder = 3

        val image = Toolkit.getDefaultToolkit().getImage(initIcon(iconSize, iconBorder).toUri().toURL())
        val popup = PopupMenu()

        val openItem = MenuItem(BTN_OPEN)
        val exitItem = MenuItem(BTN_EXIT)
        openItem.addActionListener { lmbClickListeners.forEach { it.invoke() } }
        exitItem.addActionListener { exitProcess(0) }
        popup.add(openItem)
        popup.add(exitItem)

        val trayIcon = TrayIcon(image, "Citeck Launcher", popup)

        trayIcon.addMouseListener(object : MouseListener {
            override fun mouseClicked(e: MouseEvent) {
                if (e.button == 1) {
                    lmbClickListeners.forEach { it.invoke() }
                }
            }
            override fun mouseEntered(e: MouseEvent) {}
            override fun mouseExited(e: MouseEvent) {}
            override fun mousePressed(e: MouseEvent) {}
            override fun mouseReleased(e: MouseEvent) {}
        })
        systemTray.add(trayIcon)
    }

    private fun initIcon(size: Float = 24f, border: Int = 0): Path {

        var pngData = ImageUtils.loadPng("classpath:logo.svg", size)
        if (border > 0) {
            pngData = ImageUtils.addTransparentBorderToPng(pngData, border)
        }

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
