package ru.citeck.launcher.view.tray.gtk

import com.sun.jna.Native
import com.sun.jna.Pointer
import kotlin.system.exitProcess

object GtkTrayIndicator {

    private val references = ArrayList<Any>()

    private lateinit var gtk: GtkLib
    private lateinit var glib: GLib

    fun loadLibraries() {
        gtk = Native.load("gtk-3", GtkLib::class.java)
        glib = Native.load("glib-2.0", GLib::class.java)
    }

    fun create(iconPath: String, onLmbClick: () -> Unit) {

        gtk.gtk_init(null, null)
        val statusIcon = gtk.gtk_status_icon_new_from_file(iconPath)
        references.add(statusIcon)
        gtk.gtk_status_icon_set_visible(statusIcon, true)

        val menu: Pointer = gtk.gtk_menu_new()
        references.add(menu)
        val exitItem: Pointer = gtk.gtk_menu_item_new_with_label("Exit")
        references.add(exitItem)
        val exitCallback = object : ActivateCallback {
            override fun invoke(widget: Pointer?, data: Pointer?) {
                exitProcess(0)
            }
        }

        references.add(exitCallback)
        gtk.g_signal_connect_data(
            exitItem,
            "activate",
            exitCallback,
            null,
            null,
            0
        )

        gtk.gtk_menu_shell_append(menu, exitItem)
        gtk.gtk_widget_show_all(menu)

        val statusIconListener = object : ButtonPressHandler {
            override fun callback(widget: Pointer?, event: Pointer?, user_data: Pointer?) {
                val btn = event?.getByte(52)?.toInt() ?: -1
                if (btn == 1) { // LMB
                    onLmbClick()
                } else if (btn == 3) { // RMB
                    gtk.gtk_menu_popup_at_pointer(menu, Pointer.NULL);
                }
            }
        }
        references.add(statusIconListener)
        gtk.g_signal_connect_data(
            statusIcon,
            "button-press-event",
            statusIconListener,
            null,
            null,
            0
        )

        Thread.ofPlatform().start {
            // Run the GLib main loop to handle GTK events
            glib.g_main_loop_run(glib.g_main_loop_new(null, false))
        }

        Runtime.getRuntime().addShutdownHook(Thread {
            gtk.gtk_widget_destroy(menu)
            gtk.g_object_unref(statusIcon)
            references.clear()
        })
    }
}
