# 2.2.2
## Fixes

- `citeck install` no longer prints "Daemon did not become ready"
  while the daemon is in fact booting. The wait window for the
  daemon socket is extended from 30 seconds to 3 minutes, which
  covers first-time bundle clone and snapshot auto-import on slow
  networks. A progress indicator appears during the wait.
