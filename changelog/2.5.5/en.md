## New features
- The launcher now warns you when the Docker data directory is low on disk, before containers start failing.

## Fixes
- "Force Update and Start" now responds instantly and forces a fresh check for new bundle versions over Git, while reusing release images that are already downloaded instead of re-pulling them.
- Apps that got stuck while stopping now recover on their own instead of freezing the namespace.
- A namespace reload can no longer hang indefinitely if a Git operation stalls.
- Automatic container restarts reuse the local image, so they never silently change version or fail during a registry outage.
- Leftover Docker containers, volumes and networks from deleted namespaces are cleaned up automatically on startup.
- The active namespace always shows its own containers, with no namespace mismatch.
- Extra windows (logs, editor) now close when you minimize the app to the tray or return to the Welcome screen.

## Changes
- Namespaces are edited through the form; the raw-YAML editor and the gear's right-click menu were removed.
