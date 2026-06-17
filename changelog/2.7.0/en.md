## New features
- Edit an app's configuration and its mounted files directly — even while the namespace is stopped. Your edits are saved as deltas over the generated config and re-applied on every regeneration, so new image and bundle versions still flow through while your changes stick.
- Change gutter in the editor marks edited and added lines; click a marker to revert that line to the generated default.
- Image details: view an image's digest (sha256), size and platform from the app panel, and pull it explicitly — even a release tag. Apps using the image update automatically after the pull.
- See each app's live memory usage and limit in the detail panel.
- Download an app's or the daemon's logs straight to your Downloads folder, with a notification and an "open folder" button.

## Updates
- Stronger protection for your master password (Argon2id key derivation); existing secrets keep working.
- "Update and Start" now refreshes the bundle repositories before starting; the bundle picker lists every configured repository and lets you refresh the selected one.
- Clearer secrets dialogs: no "Skip" once a master password is set, the migration dialog offers a reset, and creating a password has a Cancel button.
- More readable logs: level colours tuned for the light theme and monospace configuration-error messages.

## Fixes
- A detached app no longer gets stuck "queued" after a force update.
- A manual restart or a configuration apply no longer inflates the restart counter; the count in the panel matches the app-table badge.
- The editor no longer flashes a black or white background when it opens.
- Context menus: the first click that closes a menu no longer selects an app or shifts the row, and opening a menu no longer nudges the row height.
- The Stop button is shown while an app is updating and disabled while it is stopping; the app-table header stays pinned while scrolling.
- On desktop, closing the app now reliably stops the background daemon on all platforms.
