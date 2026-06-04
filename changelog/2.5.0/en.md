## Improvements
- Desktop namespaces are now stored in the database for reliable, consistent storage.
- "Check for updates" is always available from the desktop update menu.

## Fixes
- Snapshot import and export now work in the desktop app — volume data is restored correctly.
- Deleting a namespace no longer leaves a leftover entry behind.
- Desktop app icon now shows in the application menu after install on Linux.
- Dropdown menus no longer get clipped inside dialogs, panels, and the setup wizard.
- The Windows executable now ships with the application icon.
- Upgrade from the 1.x launcher is more robust: a failed data migration now retries cleanly instead of leaving an empty state.
