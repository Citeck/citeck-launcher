# Resumable Launcher update downloads

Baseline: Launcher 2.16.2, commit
245cd7573ae1fdb68098c4091534cf56bef6808c with cumulative local changes.
The user explicitly requested automatic updater download retries with resume,
then clarified that resumed bytes must reset retry counters so poor connections
can complete. The latest clarification excludes servers that ignore Range:
only new bytes in a requested 206 resume reset counters in both downloaders.

Update release assets (payload plus SHA256/signature sidecars) retain an owned
random partial file during one staging call. Transient request/body errors and
HTTP 408/429/5xx retry after 3 seconds. Three consecutive failures without resumed progress end the operation; counters
reset on nonempty writes from a requested 206 response. Full 200 transfers never
reset counters. There is no lifetime attempt cap while partial resumes progress. The separate 30-second
inactivity deadline remains; active transfers have no total duration deadline.
Caller cancellation interrupts both reads and retry waits.

Retries request Range from the persisted byte offset and If-Range when a strong
ETag or Last-Modified validator is available. Each attempt starts with the release
asset URL, allowing fresh redirects rather than reusing expiring redirected URLs.
A 200 response truncates and restarts the partial file; a 416 resets the offset
and the next attempt requests the whole file. 206 Content-Range start, total,
length and validator are checked. Encoded representations are refused; request
Accept-Encoding is identity. Invalid ranges, changed partial-response validators,
permanent HTTP errors, local file errors and oversized bodies fail immediately.
The existing 500 MiB download cap includes resumed bytes and rejects oversize
responses rather than silently accepting a capped prefix.

The destination is replaced only after a complete transfer. All partial updater
files are removed on terminal error/cancellation; a later independent manual
attempt starts fresh. Byte progress starts at the resumed offset and truthfully
resets on a whole-file restart. Stage still verifies SHA256 and the mandatory
configured signature before extraction/manifest selection. Integrity mismatch is
not retried. Transport retries never repeat apply or bypass failed-version health
or rollback policy.

Snapshots count actual bytes received during each attempt, excluding the initial
callback reporting an existing partial offset. Both counters reset only on received data from a requested 206 resume. When a
server ignores Range and responds 200, bytes do not reset the counters, even if
the restarted file grows larger than on the preceding attempt. Three consecutive
failures without resumed progress terminate; SHA mismatch remains immediate.

Verification evidence is in Solution `.tmp/update-download-retries/`:
real HTTP tests cover 110 progressing failures for each downloader, range resume,
200 restart, changed asset restart, 416 reset, invalid ranges/validators, 404/403
immediate failures, bounded 503/429 failures, inactivity resume, cancellation and
known/unknown-size cap rejection. The full Stage test interrupts an in-memory
signed archive, resumes it after the real 3-second delay, observes byte progress
while pending, verifies SHA256/signature and selects the extracted binary.
Synthetic keys exist only in test process memory; no production credentials used.
The UI code did not change; existing component screenshots remain applicable.

Knowledge dependencies: Launcher revision 4, mandatory gateway revision 3, no
blockers. No commit, release, deployment or installation performed. Preserve new
`internal/update/download.go`, `download_test.go` and the preceding
`internal/downloadhttp` helper files hidden by the existing local exclude; include
them explicitly only after later commit authorization. Excludes were not changed.

Final checks: full affected race/coverage PASS (updater 83.6%, snapshot 77.6%,
shared inactivity helper 88.7%); golangci-lint 2.11.4: 0 issues; forced native
desktop/server builds and TypeScript/Vite PASS. Existing Vite chunk warning
remains. Git diff check PASS; HEAD unchanged and index empty.
Finding `p-90814618d1d07711756b4f3e479dfae8` is pending curation; report `todo-9a0798dfec45ef6f5fe7469316fae84a`
records the pre-correction improvement, not a deployed resolution.

## Range accounting correction

The latest user clarification excludes non-resumable full-file restarts from
counter reset. Initial full 200 attempts and repeated full 200 attempts count as
failures if interrupted, even when bytes arrive or the restarted prefix becomes
larger. Only received bytes in a requested 206 resume reset the counters. Successful
complete 200 downloads remain valid; a server ignoring Range can still finish
within the bounded attempts. A 416 followed by a full restart also does not reset
counters on interrupted 200 responses.

New real HTTP regression tests cover both equal-size and increasing-size repeated
200 prefixes for both downloaders: exactly three failed attempts, without needing
context cancellation. Updater partial files are cleaned; incomplete snapshot files
are not promoted. Existing 110 progressing 206 attempts still complete, preserving
long-lived useful resume. Evidence: Solution `.tmp/download-range-accounting/`.

Range correction final checks: full affected race/coverage PASS (updater 83.7%,
snapshot 77.7%, helper 88.7%); pinned lint 0 issues; forced desktop/server builds
PASS. Finding `p-8165a9c2e90d57c0aa2a06cd885fd4ab` pending; report `todo-5bbdbb373ffdfc0b17e168d5adb208c2`.
Everything remains local/uncommitted/unreleased/undeployed; index empty.

## Cumulative review follow-up

The completed [cumulative review](cumulative-review-2026-10-12.md) corrected the
remaining total 120-second apply request/response limits, snapshot partial-response
validation, and short chunked updater range handling with unknown totals. Both
downloaders share validated range parsing; invalid snapshot ranges never append
or reset counters, and short updater spans retry from the actual received offset.
Final review tests include executed mutations and a real UpdateDialog/API browser
preview with a pending apply POST after 181 seconds of simulated browser time.
The real Unix HTTP staging test outlives a scaled ordinary response-write budget.
All checks are local evidence, not installation/deployment or release approval.
