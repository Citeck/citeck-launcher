## Fixes
- A dependency whose data volume was deleted (for example, from the Volumes page) is no longer offered as an upgrade whose migration then fails: with no data left to protect, the bundle's version applies on the next start.
- `citeck deps` now says to re-run `install.sh` when a migration needs a newer launcher; `citeck update` only refreshes workspace and bundle definitions.
- The desktop warning before deleting a namespace now says that its data volumes and folder, including snapshots, are deleted too.
