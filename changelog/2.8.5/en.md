## Changes
- The app list is cleaner: the Ports column and the column-header row are gone.
- The sidebar's "Apps" and "Resources" headers are now translated in every language.

## Security
- Server mode: the launcher's built-in Web UI can no longer be exposed over TCP. It was never a supported server interface — the CLI/TUI is — and it is now disabled in code, not just off by default.
