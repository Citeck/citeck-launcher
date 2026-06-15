## New features
- Reusable registry credentials. A private-registry login you save can now be reused across namespaces and workspaces instead of being re-entered for each one. Before a namespace starts, the launcher also checks that every private registry it needs has a credential, so a missing login is caught up front instead of stalling a download later.

## Changes
- The bundle picker no longer has a separate "LATEST" entry: the newest version is labelled "(LATEST)" and a namespace is always pinned to a concrete version, so it never switches to a newer version on its own.
