## Fixes
- **Upgrading from 1.x no longer brings back namespaces you had deleted.** They appeared in the list as bare ids with no name and no bundle, and opening one reported `namespace "..." not found`. Deleting a namespace in 1.x left part of its state behind, and the upgrade turned each leftover into an entry of its own. If your list already shows these, delete them with the bin icon — nothing else is affected.
- Namespaces in the legacy default workspace keep their stopped-app state and their remembered bundle after the upgrade.
