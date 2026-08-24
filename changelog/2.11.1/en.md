## Fixes
- **Windows: no more console window next to the launcher.** The app opened a second, black window on every start; it is gone, and the launcher's own log now goes to `launcher.log` in the log folder instead.
- **Creating a master password now tells you when the two entries do not match.** The check was already refusing to continue, but said nothing — so the Confirm button looked like it did nothing at all.

## Improvements
- Updated the Git library to 5.19.2, which closes two vulnerabilities in repository handling: a path traversal through crafted reference names, and worktree operations following symbolic links.
