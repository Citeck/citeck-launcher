## Fixes
- The AI service (call recording / speech-to-text) is now reachable through the proxy, and the speech-to-text sidecar wiring is applied correctly.
- Attaching or detaching AI, the speech-to-text sidecar, or OnlyOffice at runtime now takes effect immediately — the proxy and AI links update without recreating the namespace.
- External microservices running outside Docker can again reach the desktop cloud configuration server (to obtain RabbitMQ, ZooKeeper, and database addresses); it was mistakenly listening on loopback only.
- The "enter registry credentials" prompt now reliably reappears when an image pull fails with an authentication error, even if the one-time notification was missed.
- Deleting a registry credential no longer leaves a dangling "(not found)" binding for its host.
- The workspace configuration editor window is larger and easier to read, and "Reset to git" now applies in one click.
- CPU and memory cells no longer keep a stuck highlight after an accidental selection.
- The Windows installer no longer hangs on "Cancelling…" after an install or upgrade, which could block other installers.
