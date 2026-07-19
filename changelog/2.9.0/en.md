## New
- **HTTPS for a namespace (self-signed).** A new "Enable HTTPS (self-signed)" option in the namespace dialog serves the proxy over HTTPS with an automatically generated certificate. The port follows the scheme automatically — 443 for HTTPS, 80 for HTTP — so turning HTTPS on and off just works. Your browser shows a one-time warning for the self-signed certificate.

## Changes
- **Config edits are surgical and instant.** Editing a namespace's or an app's settings now recreates only the containers whose configuration actually changed, and reflects the change immediately — it no longer re-pulls images for unrelated services. To pick up the latest images, use "Update & Start".
- **Startup waits for the master password.** When a namespace pulls from a private registry and your secrets are locked, it now waits for you to enter the master password before starting — so the credentials prompt no longer hides the unlock screen.
