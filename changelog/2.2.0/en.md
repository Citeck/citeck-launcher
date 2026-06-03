## Dev workflow

- `:snapshot` images are now re-fetched from the registry on every
  `citeck start` / `citeck reload`. If a developer pushes a new image
  under the same `*-snapshot` tag, the reload will actually apply it —
  previously the tag-match against a stale local digest kept the old
  container running silently.

## Responsiveness and reliability

- Namespace status updates land within ~100ms of an event (previously up
  to 5 seconds), so `citeck status --watch` and the web UI reflect
  transitions live.
- Graceful shutdown no longer holds an internal lock during Docker
  network cleanup — `citeck status` / `citeck apps` stay responsive
  until the namespace is fully stopped.
- Stop-timeout budget is computed per-app from its configured
  `stopTimeout` plus a safety margin instead of a flat 10s. Java
  webapps with slow SIGTERM handlers no longer end up in
  `STOPPING_FAILED` and no longer block the shutdown chain.
- Rapid repeated commands (`citeck reload` × 3, `citeck stop` × 2) are
  coalesced automatically; duplicates don't produce duplicate work or
  duplicate history entries.
- Operator-initiated commands that collide with an in-flight namespace
  shutdown are rejected with a clear error instead of leaving the
  daemon in a stuck state.

## Observability

- Pull progress surfaces a stall warning after 5 minutes with no
  registry activity, matching the pre-rewrite behavior.
- Post-start init actions (e.g. postgres DB creation) run off the main
  runtime loop, so they can't starve other state transitions while the
  action itself is blocked on a slow container.

## TUI

- `charmbracelet/huh` replaced by an internal `internal/cli/prompt`
  toolkit built directly on bubbletea. Supersedes the huh-based prompts
  from 2.1.0 (Select viewport fix, heap-guard `huh.NewNote`, keymap
  wrapping for Esc). Behavior and UX are preserved; `huh` and its
  transitive deps are no longer in `go.mod`.
