## New features
- **Infrastructure versions are now tied to your data.** A bundle update can no longer move PostgreSQL, RabbitMQ, ZooKeeper, Keycloak or MongoDB to a version that would break the data an existing namespace already has. The launcher keeps running the version the data was created with and reports the newer one instead — as a banner and the new **Dependencies** dialog in the desktop app, and as `citeck deps` on a server.
- **PostgreSQL 17 → 18 migration**, from the Dependencies dialog or with `citeck deps upgrade postgres`. The namespace is stopped, the data is dumped, a new cluster is built in a **new** volume, restored and verified, and the namespace is switched over — with progress for every step. Anything that goes wrong is rolled back to the old data automatically.
- The previous data volume is **kept** after a successful migration (it is listed on the Volumes page); delete it yourself once you are satisfied with the result.
- If a target volume left over from an earlier attempt is still there, the launcher shows its size and version and deletes it only after you confirm.

## Changes
- New namespaces are created with PostgreSQL 18; existing ones stay on PostgreSQL 17 until you migrate them.
- Upgrades of RabbitMQ, ZooKeeper, Keycloak and MongoDB are reported but not performed yet — they wait for a launcher release that can migrate them.
- Changing a dependency's image to a version that would break its data is refused, in the config editor and in `citeck edit`.
- Starting, reloading or deleting a namespace is refused while a snapshot or a dependency migration is running, and the message says which one.
