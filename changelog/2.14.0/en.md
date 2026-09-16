## New features
- **Qdrant now publishes its gRPC port (6334)**, the port `rag` actually talks to — 6333 only ever carried the health check. A `rag` you run outside the launcher (from an IDE) now reaches this stand's vector store with no configuration at all: its defaults already point at `localhost:6334`.

## Changes
- **Stopping `rag` no longer deletes Qdrant.** The store stays in the namespace as a stopped app with a start button, which is what running `rag` yourself needs — stop it here, start it there, and the data it indexed is still reachable. It never starts on its own: on every stand where RAG is simply switched off, Qdrant stays stopped and costs no memory, exactly as before. Start it deliberately (`citeck start qdrant`) and it stays up until you stop it or restart the launcher.
- **The same for `ai` and its speech-to-text sidecar.** Stopping `ai` keeps the sidecar described and stopped instead of removing it, so an `ai` run from an IDE still has speech recognition to reach on localhost.
- **The AI assistant's RAG flag now follows whether the namespace HAS `rag`**, not whether `rag` happens to be running. Toggling `rag` no longer rewrites and recreates the `ai` container.
