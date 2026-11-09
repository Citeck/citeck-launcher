## New features

- Connect local AI agents via MCP to inspect status and logs; Developer access also allows managing applications, editing runtime settings and Spring configuration, and collecting JVM diagnostics.
- Manage agent access in desktop Settings: approve OAuth sign-ins or create manual tokens, choose Observer or Developer, set expiry and revoke access. MCP is disabled by default and listens only on loopback when enabled.
- Set per-application CPU limits in cores, including fractional values, through the application configuration editor.

## Fixes

- Reload now reports generated configuration file write failures instead of continuing with files that were not saved.
- Edited configuration files are no longer silently replaced with empty content when the current file is unavailable and the saved edit cannot be reapplied.
- YAML configuration edits preserve all documents in multi-document files.
- JVM diagnostic commands are no longer automatically repeated after they may already have reached the JVM.
