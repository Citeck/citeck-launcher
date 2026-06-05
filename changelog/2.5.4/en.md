## Fixes
- Namespaces migrated from version 1.x are no longer corrupted on first start: an unwanted automatic snapshot re-import that could break the database was removed. Importing a snapshot is now always a manual action.
- After switching the active namespace, the app now shows that namespace's own containers — the header and the application details no longer mismatch.
- Deleting a namespace now also removes its Docker volumes, network and containers, so leftover data no longer accumulates.
- Container CPU usage is displayed correctly again (it was stuck at 0%).
- The log viewer no longer freezes after a container restart — it reconnects automatically.
- The create/edit namespace form is tidier: bundle repositories with no releases are hidden, and the latest version is selected automatically when you change the repository.
- The desktop window title now reflects the running version.
- Daemon logs are kept in a single "logs" folder (matching version 1.x) instead of being split across two folders after an upgrade.
