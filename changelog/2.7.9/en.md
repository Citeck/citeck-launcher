## New features
- **Additional apps in the workspace config.** Declare any custom container — a mock or simulator, an auxiliary service, a sidecar — directly in the workspace config (`workspace-v1.yml` → `additionalApps:`), with no change to the launcher. Defined once, an additional app is distributed to every namespace that uses the workspace and applied on the next reload, exactly like the built-in `webapps`. The full container surface is supported: image (including bundle-style `<repoId>/path:tag` registry resolution), environment variables with `${VAR}` templating, command, ports, volumes, `dependsOn`, init containers, init actions, probes, resources, shared-memory size and stop timeout.

## Changes
- An app whose `dependsOn` references a service that is not deployed is now excluded from the namespace (transitively) instead of starting without its dependency.
- Web applications no longer depend on Keycloak; only the proxy and the model service do, and only when Keycloak authentication is enabled. Upgrading to this version recreates the web-application containers once (their deployment hash changes) — no action needed.
