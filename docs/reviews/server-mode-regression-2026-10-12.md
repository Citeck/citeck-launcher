# Server-mode regression verification

The user requested a fresh server-mode check and main scenarios on the test
stand. SSH access was verified using the existing credential reference; no
credential values, cookies or JWTs were retained in evidence.

## Released regression and correction

The signed `v2.17.0` Linux amd64 payload, commit
`d48cc7abc9ca47e7ce13516380b8c91543b4c77a`, was installed through its normal
`install --yes` binary lifecycle. All 13 existing container IDs and states were
preserved during the binary swap. The namespace configuration remained unchanged.

A full namespace stop/start exposed a HIGH regression: ordinary server start
refused the persisted bundle when the pinned `enterprise:2026.2.1-s3-live` file
was no longer present in the repository. The acknowledged start left the
namespace STOPPED with a missing-bundle `updateError`.

`invokeReloadEx` treated `refreshImages` as explicit Git synchronization intent
in both modes. It now requires synchronization for `forceGitPull`, or for
`refreshImages` in desktop mode. An ordinary server lifecycle start retains the
persisted namespace bundle fallback. Explicit Git refresh and desktop Update &
Start remain strict; this does not silently report a failed refresh as successful.

The regression test uses real resolver and persisted state I/O. Its
ordinary-server case fails against the released source and passes with the fix;
its forced-server and desktop cases still require failure. A smaller-cache
runtime guard keeps this local fixture free of Docker lifecycle work. Actual
STOPPED-to-RUNNING startup was independently verified on the stand.

## Verified scenarios

- DB, S3 and external SMTP generator comparison against 2.16.2: identical
  container definitions and generated files (12/13, 12/13 and 11/13).
- Live headless discovery identifies server mode, with 107 routes and 35
  contracts; desktop focus and updater routes are absent. No server Web UI or
  cloud-config TCP listener was enabled.
- Reload dry-run reports 13 kept, 9 detached and no create/recreate/remove.
- Mailpit stop persists `detached: true` through reload; explicit start restores it.
- eapps restart completes and S3 stays connected.
- Full namespace stop reaches STOPPED; corrected ordinary start restores all
  original 13 RUNNING and 9 intentionally STOPPED applications.
- systemd daemon restart preserves all 13 container IDs and start times.
- Original image IDs/tags, persistent bind mounts, published ports and
  namespace/daemon configuration hashes are preserved. Docker reallocates
  image-declared anonymous ZooKeeper mount IDs during a full container recreation;
  its authoritative `/citeck/zookeeper` bind remains unchanged. No broad cleanup
  was performed.
- Login through the original Keycloak and actual dashboard rendering succeed.
  Screenshots were inspected; no HTTP 5xx response was observed in the browser.
- Existing S3-backed 64 KiB and 1 MiB test files from two stores still match their
  recorded SHA256 through browser-authenticated platform downloads.
- ecos-content 1.7.2 S3 synchronous 1 MiB and multipart 11 MiB + 123 bytes
  (three chunks) upload/download hashes match; multipart abort succeeds. Both
  newly created synthetic objects were deleted through ecos-content.
- Configuration validation and snapshot listing work. No snapshots existed;
  export/import were not performed. `health` remains degraded solely because of
  the nine deliberately stopped apps, with no failed apps.
- Four affected Go package suites pass with race/coverage (daemon 69.8%, CLI
  42.8%, bundle 82.0%, namespace 84.2%); Go vet passes, golangci-lint reports zero
  issues, and the native server build succeeds. No frontend source changed.

## Historical boundary

The user subsequently authorized deleting this disposable test installation.
Its original platform data and binary backups were intentionally removed for
the clean-install phase; they are no longer available on the stand. See
[clean-server-verification-2026-10-13.md](clean-server-verification-2026-10-13.md)
for the current installation and evidence. The preservation checks below
describe the earlier verification boundary.


At that earlier boundary, the test stand ran `dev-20261012-server-fallback`,
built from the release plus this local correction. Original and released binary
backups were retained then; the later authorized clean reset removed them. Public `v2.17.0` contains the regression. The subsequent user-authorized
`v2.17.1` hotfix includes the correction, the ten-case regression test and
verification documentation. The test is explicitly tracked despite the existing
local git exclude; exclude rules are unchanged.

Evidence: Solution `.tmp/server-mode-recheck-20261012/`. Knowledge used Launcher
revision 4, mandatory gateway revision 3, installation revision 1, server CLI
revision 2, troubleshooting revision 2, S3 configuration revision 2, content-store
configuration revision 1 and store-selection revision 1. Applicable contexts had
no blockers. Finding `p-fc53e7a6de7d377b4f91dd573e2a6c0b` remains pending curation;
report `todo-a4da7fd6d4b5f2f50cf80e8a2738c4a7` records the released defect, not a
curated resolution.
