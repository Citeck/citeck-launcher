## New features
- **A bundle can require a minimum launcher version.** Creating or editing a namespace is now refused if the selected bundle needs a newer launcher than the one you are running — the message names the launcher version to update to. A namespace already running such a bundle is unaffected, and choosing "LATEST" always picks the newest bundle this launcher can actually run.
- **The namespace settings gear now shows a dot when a newer bundle is available** in your bundle repository — green if this launcher can run it, amber if a newer launcher is needed first. Hover the gear to see the version and what to do.

## Fixes
- **"Force Update" now really re-reads the workspace repository.** It used to report success without fetching anything for up to an hour after the previous sync, so a bundle version pushed a moment earlier stayed invisible in the version list and to the "newer bundle" dot.
- **The ↻ button next to the bundle list now refreshes the repository the launcher actually reads.** When the bundle repositories live inside the workspace repository — the standard layout — it used to clone a second, unused copy per repository and report success while nothing changed on screen.
