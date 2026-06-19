## New features
- Edit the workspace configuration (workspace-v1.yml) right from the Welcome screen — a gear next to the workspace selector opens a YAML editor with a change gutter. Your edits are saved as a delta over the git reference and re-applied on every sync.
- Choose exactly which volumes to include when creating a snapshot.

## Fixes
- Desktop statuses now update in real time on every platform; on Windows they no longer appeared frozen (for example all "Loading") during a long startup until you pressed Stop.
- On Windows, the window no longer becomes unclickable after confirming the master password or closing a nested dialog.
- Image-pull failures now record the underlying cause in the logs (for example a refused registry connection), so it is clear why a pull did not go through.
- The namespace list shows localized statuses, and deleted namespaces disappear from the quick-launch list.
- The Welcome screen is legible in the light theme, and native checkboxes render correctly and aligned in both themes.
- The namespace header updates reliably after a rename, and picking a value in a dropdown no longer shifts the layout.
- The installer now launches the app automatically after a Windows install or upgrade.
