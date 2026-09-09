## New features
- **RAG semantic search.** Enterprise bundles now offer a `rag` app for semantic search over your knowledge base, off by default. Starting it brings up a Qdrant vector database automatically and turns on knowledge-base access for the AI assistant; a disabled `rag` still uses no extra memory — Qdrant isn't even created. Community bundles are unaffected — neither `rag` nor Qdrant appear on them.
- **Configurable webapp dependencies.** A webapp's `dependsOn` can now be extended through the workspace's `webapps[].defaultProps.dependsOn` and a namespace's `webapps.<id>.dependsOn` — configured dependencies add to the built-in ones, they don't replace them. A `dependsOn` cycle now fails generation with a clear error instead of leaving the apps involved waiting forever.

## Changes
- **A stopped dependency no longer lets its dependent start past it.** Previously, an app whose dependency was manually stopped would start anyway and then fail its health checks. It now waits, showing what it's waiting for in its status, and proceeds once the dependency is started.
- **A bundle-pinned image can no longer be overridden by workspace or namespace configuration.** The bundle always wins now; to run a different image on one stand, use `citeck edit <app>` explicitly. Configuration that still tries to override the image is logged as a warning instead of silently taking effect.

## Fixes
- **Toggling alfresco now regenerates the namespace**, like onlyoffice and ai already did since 1.4.1. Previously the proxy kept treating alfresco as available (or not) after a manual stop/start, until the next unrelated reload caught up.
