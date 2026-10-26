## Fixes
- Snapshot import and export show live progress and recover completion after a connection interruption, without leaving the loading screen stuck.
- PostgreSQL migration after snapshot restore recognizes legacy data layouts and downloads a missing source image automatically.
- Unlocking the vault refreshes stopped namespaces so S3 storage and other secret-dependent applications become available without another restart.
- Startup banners stay hidden until master-password entry and the subsequent refresh finish.
- Dependency migrations and rollbacks no longer leave a completed final step spinning or namespace controls blocked.
- Desktop events keep their original order; normal state synchronization no longer produces repeated “Connection restored” notifications.
- Alfresco Share administration tools work again on stands that include Alfresco.
- HTTPS verification rejects known compromised intermediate certificates.
