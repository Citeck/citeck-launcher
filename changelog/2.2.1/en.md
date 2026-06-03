# 2.2.1
## Fixes

- `citeck start` immediately after `citeck stop` no longer prints
  "all apps started" while the platform is still stopped. The wait
  loop now waits for the daemon to begin the new start cycle before
  evaluating terminal state.
