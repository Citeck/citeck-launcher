## Fixes
- Quick Start "With Demo Data" now actually loads the demo data — previously the namespace could start with empty databases.
- The namespace created by Quick Start is named "Citeck Default" again, instead of taking the button's label.
- The dashboard and namespace list now show the real bundle version (for example 2026.3-RC1) instead of "LATEST".
- "Check for updates" no longer shows a 404 error when you are already on the latest version.
- Fixed RabbitMQ authentication errors after a server restart ("cannot load menu"): the RabbitMQ container had too little memory to finish setting up its service account and got a new identity on every restart. It now keeps a stable identity and enough memory.
- The diagnostics archive (dump-system-info) now redacts passwords, tokens and other secrets from container environment variables, logs and config files.
- The web UI is now fully translated in Chinese, Spanish, German, French, Portuguese and Japanese — many strings that previously appeared in English are now localized.
- Restoring a namespace snapshot no longer corrupts the database — the restore now starts from a clean volume instead of merging over the existing data.
