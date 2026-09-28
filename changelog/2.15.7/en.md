## Fixes
- Upgrading from an early 1.x launcher no longer stops the launcher from starting: a namespace that stored its users as one "name:password" line is migrated with those user names.
- A proxy image set in such a namespace is carried over instead of being lost.
- A namespace migrated from a launcher older than 1.3.6 keeps its last resolved bundle, so it still starts if that bundle has since moved in the bundle repository.
