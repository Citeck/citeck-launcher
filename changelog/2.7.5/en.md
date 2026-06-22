## Fixes
- Stopping a namespace now removes all of its containers cleanly — a slow-stopping app (such as eapps) no longer lingers in the "Exited" state after shutdown, and the namespace network is removed reliably.
- Deleting a workspace now reclaims everything inside it — every namespace's Docker data volumes (PostgreSQL, MongoDB, …) and database records, not just the on-disk files.
- The desktop's built-in configuration server now runs only while a namespace is active and frees its port when stopped, so restarting the app over a stopped namespace no longer fails with "address already in use".
