package ru.citeck.launcher.view.tray.gtk

import com.sun.jna.Callback
import com.sun.jna.Library
import com.sun.jna.Pointer

/**
 * Interface for working with the GTK library.
 */
interface GtkLib : Library {
    /**
     * Initializes GTK.
     *
     * @param argc Pointer to the argument count (can be null).
     * @param argv Pointer to the array of arguments (can be null).
     */
    fun gtk_init(argc: Pointer?, argv: Pointer?)

    /**
     * Creates a new empty GTK menu.
     *
     * @return A pointer to the created menu.
     */
    fun gtk_menu_new(): Pointer

    fun gtk_widget_show_all(widget: Pointer?)

    /**
     * Creates a new menu item with the given label.
     *
     * @param label The text label for the menu item.
     * @return A pointer to the created menu item.
     */
    fun gtk_menu_item_new_with_label(label: String): Pointer

    /**
     * Adds a menu item to a menu container.
     *
     * @param menu A pointer to the menu container.
     * @param menuItem A pointer to the menu item to be added.
     */
    fun gtk_menu_shell_append(menu: Pointer, menuItem: Pointer)

    /**
     * Connects a signal to the specified GTK object.
     *
     * @param instance Pointer to the GTK object.
     * @param detailed_signal The name of the signal (e.g., "activate").
     * @param c_handler The callback function implemented as a Callback.
     * @param data User data to pass to the handler (can be null).
     * @param destroy_data Function to free the data (can be null).
     * @param connect_flags Connection flags (usually 0).
     * @return The signal connection ID.
     */
    fun g_signal_connect_data(
        instance: Pointer,
        detailed_signal: String,
        c_handler: Callback,
        data: Pointer?,
        destroy_data: Pointer?,
        connect_flags: Int
    ): Long

    fun gtk_status_icon_new(): Pointer

    fun gtk_status_icon_new_from_file(fileName: String): Pointer

    fun gtk_status_icon_set_from_icon_name(icon: Pointer, iconName: String)

    fun gtk_status_icon_set_visible(icon: Pointer, visible: Boolean)

    fun gtk_menu_popup_at_pointer(menu: Pointer?, trigger_event: Pointer?)

    fun gtk_widget_destroy(widget: Pointer)

    fun g_object_unref(obj: Pointer)
}

/**
 * Interface for working with the GLib library.
 */
interface GLib : Library {
    /**
     * Creates a new main loop.
     *
     * @param context The context for the main loop (can be null).
     * @param isRunning Flag indicating whether the loop should start immediately.
     * @return A pointer to the created main loop.
     */
    fun g_main_loop_new(context: Pointer?, isRunning: Boolean): Pointer

    /**
     * Runs the main loop.
     *
     * @param loop A pointer to the main loop to run.
     */
    fun g_main_loop_run(loop: Pointer)
}

/**
 * Callback interface for handling the "activate" signal when a menu item is clicked.
 *
 * This interface is used to define a function that will be called when the menu item is activated (for example, when clicked).
 */
interface ActivateCallback : Callback {
    /**
     * The callback method invoked when the menu item is activated.
     *
     * @param widget Pointer to the activated widget (menu item).
     * @param data User data passed during signal connection.
     */
    fun invoke(widget: Pointer?, data: Pointer?)
}

interface ButtonPressHandler : Callback {
    fun callback(widget: Pointer?, event: Pointer?, user_data: Pointer?)
}
