package ru.citeck.launcher.view.tray.gtk

import com.sun.jna.Native
import com.sun.jna.Pointer
import ru.citeck.launcher.view.tray.CiteckTrayItem

@Suppress("LocalVariableName")
object GtkTrayIndicator {

    private val references = ArrayList<Any>()

    private lateinit var gtk: GtkLib
    private lateinit var glib: GLib

    fun loadLibraries() {
        gtk = Native.load("gtk-3", GtkLib::class.java)
        glib = Native.load("glib-2.0", GLib::class.java)
    }

    fun create(iconPath: String, items: List<CiteckTrayItem>, lmbAction: () -> Unit) {

        gtk.gtk_init(null, null)
        val statusIcon = gtk.gtk_status_icon_new_from_file(iconPath)
        references.add(statusIcon)
        gtk.gtk_status_icon_set_visible(statusIcon, true)

        val menu: Pointer = gtk.gtk_menu_new()
        references.add(menu)

        for (item in items) {
            val menuItem: Pointer = gtk.gtk_menu_item_new_with_label(item.name)
            references.add(menuItem)
            val itemCallback = object : ActivateCallback {
                override fun invoke(widget: Pointer?, data: Pointer?) {
                    item.action()
                }
            }
            references.add(itemCallback)
            gtk.g_signal_connect_data(
                menuItem,
                "activate",
                itemCallback,
                null,
                null,
                0
            )
            gtk.gtk_menu_shell_append(menu, menuItem)
        }

        gtk.gtk_widget_show_all(menu)

        val statusIconListener = object : ButtonPressHandler {
            override fun callback(widget: Pointer?, event: Pointer?, user_data: Pointer?) {
                val btn = event?.getByte(52)?.toInt() ?: -1
                if (btn == 1) { // LMB
                    lmbAction()
                } else if (btn == 3) { // RMB
                    gtk.gtk_menu_popup_at_pointer(menu, Pointer.NULL)
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

        Thread.ofPlatform().name("gtk-tray").start {
            // Run the GLib main loop to handle GTK events
            glib.g_main_loop_run(glib.g_main_loop_new(null, false))
        }

        Runtime.getRuntime().addShutdownHook(
            Thread {
                gtk.gtk_widget_destroy(menu)
                gtk.g_object_unref(statusIcon)
                references.clear()
            }
        )
    }
}
