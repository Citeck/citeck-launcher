## New
- **Settings page.** The gear icon now opens a real Settings page with your workspace's Git repository settings and the credentials used for private Docker registries — previously reachable only from a hover menu or from an error dialog.

## Fixes
- **macOS: "citeck-launcher is damaged and can't be opened."** Downloaded builds carried no signature, which Gatekeeper reports with that misleading message and no way past it. The app bundle is signed again, so the first launch shows the normal "unidentified developer" prompt instead — open it once with right-click → Open.
- **Upgrading from 1.x no longer loses your workspace.** When the old database could not be read, the launcher silently replaced the workspace with an empty one. The repository URL and branch are now recovered from the existing clone, and a migration that could not carry everything over says so instead of looking like a fresh install.
- **A network glitch no longer swaps a bundle repository.** A brief connection timeout during a Git pull could replace a repository with the contents of a different one. Transient network errors now keep the existing copy, and a re-clone refuses to proceed if it would install a different repository.
- **Registry credentials can be corrected.** Choosing the wrong secret for a registry left no way back. Bindings are now listed in Settings, can be re-assigned or removed, and a credential belonging to another host is flagged instead of looking valid.
- **Clearer error for an unknown bundle repository.** Instead of quietly serving the default Citeck workspace, the launcher names the repository, the ids that are declared, and where to fix them.
- **The gear icon no longer disappears.** Clicking it with no namespace open used to bounce back to the Welcome screen and hide the icon.
