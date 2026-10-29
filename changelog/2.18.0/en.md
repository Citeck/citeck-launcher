## New features
- Experimental (beta) modules: a release can ship new services as beta in the bundle's `experimental:` section. Switch them on per stand on the new "Experimental" tab of the create/edit dialog; such apps are marked BETA in the app table and in `citeck status`.
- Server mode: `citeck features` lists the beta modules the stand's bundle offers and `citeck features enable|disable <id>` switches them; `citeck install` asks which ones to enable. Switching a module off removes its containers at the next apply and keeps their data volumes.
- A beta module that a later release makes a regular one keeps running where it was enabled; elsewhere it is offered like any other new app, according to the workspace template.
- Feature names and descriptions can be supplied in multiple languages by the bundle.

## Fixes
- Exporting and deleting application files works when the Launcher data directory is reached through a symbolic link.
