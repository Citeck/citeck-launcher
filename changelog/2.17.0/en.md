## New features
- Choose DB or S3 content storage when creating a namespace; S3 availability follows the selected bundle and edition.
- Agent API and headless discovery commands expose supported Launcher operations.
- Git update recovery offers cached configuration, retry and a repository-specific token choice, with the last successful sync time.
- Updates and snapshot downloads show received bytes and resume interrupted transfers automatically.

## Fixes
- Active downloads continue while bytes arrive, without a total transfer timeout.
- Normal S3 initialization no longer shows a banner; configuration and connection errors remain visible.
- Mailpit uses host ports 17011 for SMTP and 17012 for its web interface.
- Git diagnostics hide credentials embedded in repository URLs.
