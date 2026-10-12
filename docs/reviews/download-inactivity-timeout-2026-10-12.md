# Download inactivity timeout

Inspected baseline: Launcher 2.16.2, commit
245cd7573ae1fdb68098c4091534cf56bef6808c, with cumulative local changes.
The user requested that active downloads continue regardless of total duration,
and that timeout measure only lack of received bytes.

Previously, updater payload HTTP requests had a 30-second total limit, including
response body transfer. Snapshot transfers had connection/header timeouts but no
body inactivity timeout. A stalled body could block indefinitely and never reach
the existing retry loop.

Both payload paths now use `internal/downloadhttp`: a request-scoped cancellable
context with a 30-second inactivity timer. Each nonempty body read renews the
deadline. Waiting for headers is bounded as well. The helper copies the HTTP
client and clears its total timeout only for this request; metadata requests,
transport configuration, redirects and parent cancellation remain unchanged.
Timer cleanup occurs on errors, EOF and body close. Timeout errors are identifiable
with `errors.Is(err, downloadhttp.ErrIdleTimeout)`.

Snapshots retain partial files and resume through the existing Range logic after
the existing 3-second pause. The subsequent [update retry correction](update-download-retries-2026-10-12.md)
adds updater retries and resets counters on received bytes from a requested
206 resume in both paths. A full 200 restart does not reset counters. Three
consecutive failures without resumed progress stop the transfer; progressing
partial resumes have no lifetime attempt limit. SHA mismatch is immediate. Updater
temporary files are cleaned on final failure without replacing an existing
destination. Integrity checks and update health/rollback gates remain unchanged.
No UI source changed in this phase; prior actual component screenshots remain
applicable to the unchanged presentation.

Verification evidence: Solution `.tmp/download-timeout/`. Real HTTP tests cover
an updater download lasting 34 seconds with continuous progress, a 30-second
updater stall preserving its destination and removing the temporary file, and
a snapshot stall followed by Range resume and successful SHA256 verification.
Shared-helper tests cover active transfers beyond the deadline, stalled headers,
stalled bodies and caller cancellation. Final race/coverage, pinned lint and
forced desktop/server builds are recorded there. Knowledge context used Launcher
revision 4 and mandatory gateway revision 3; no blockers. Existing download-progress
TODO concerns presentation and is separate from this timeout defect.

Everything remains local, uncommitted, unreleased and undeployed. The new helper
files are hidden by the existing local git exclude and must be explicitly included
if a later commit is authorized; the exclude was not changed.

Final results: race tests PASS (`downloadhttp` 88.7%, updater 82.3%, snapshot
77.5% statement coverage); golangci-lint 2.11.4: 0 issues; forced native desktop
and server builds PASS, including TypeScript/Vite. Existing Vite chunk-size warning
remains. `git diff --check` PASS; HEAD unchanged and index empty.
Finding `p-6e8209fcdf93060560e9ef83449bc613` is pending curation;
report `todo-a6177cf14f30efb50f81ce2ce10f83fe` records the observed defect and local
correction, not a deployed resolution.
