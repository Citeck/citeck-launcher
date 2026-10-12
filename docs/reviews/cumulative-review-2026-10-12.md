# Cumulative local changes review

Completed the user-invoked `/home/spk/.claude/skills/review-changes/SKILL.md`
workflow using Codex reviewers, as explicitly requested instead of Claude models.
Scope: all cumulative local Launcher changes and the ecos-webapp-commons discovery
correction, including new Go sources hidden by the existing local git exclude.
Bases: Launcher 2.16.2 commit 245cd7573ae1fdb68098c4091534cf56bef6808c;
commons commit 55b1734245759b46283b2270fe85ed20b88cab08. No staging, commit,
release, publication, installation or deployment occurred.

Three reviewers partitioned the initial pass, then exchanged partitions for an
independent final pass. Phase 1 found four HIGH and two LOW issues. Final-pass
iteration 1 found two additional HIGH issues; iteration 2 returned no HIGH,
MEDIUM or LOW findings and `ready_to_finalize: yes` for all three partitions.
All findings were corrected; none were deferred or dismissed. File citations
below point to the corrected boundary; original reports retain pre-fix lines.

| # | Phase/iteration | Severity | Origin | Location | Finding and correction |
|---|---|---|---|---|---|
| 1 | 1/1 | HIGH | introduced | internal/daemon/routes_config.go:535 | Queue release preceded cache capture, allowing a later cached retry to be consumed by the running pass. Target, force and cache are now claimed atomically; a latest plain request clears prior cache intent. |
| 2 | 1/1 | HIGH | touched-file | internal/git/repo.go:483 | Raw repository URLs and Git errors could expose userinfo/query credentials. Clone/pull/reclone/auth diagnostics sanitize URL/error text while preserving error identity. |
| 3 | 1/1 | HIGH | related-code | internal/daemon/desktop_update.go:62; web/src/lib/api.ts | Active staging could still exceed the UI/server 120-second total deadline. Apply has no total client deadline; its handler lifts the ordinary response-write deadline. Inactivity/caller cancellation remain. |
| 4 | 1/1 | HIGH | introduced | internal/snapshot/download.go:136 | Any 206 counted as resume without checking Content-Range, enabling repeated wrong-prefix append/counter reset. Range, representation, length and validator checks precede append/reset. |
| 5 | 1/1 | LOW | touched-file | AGENTS.md:207 | Stale guidance described normal dismissible S3 banners. Guidance now matches actionable error/configuration-only rendering. |
| 6 | 1/1 | LOW | introduced | ecos-webapp-commons/.../WebAppZkDiscoverySelectionTest.kt:19 | Three new natural-language test names conflicted with the project's camelCase convention. Renamed mechanically. |
| 7 | 2/1 | HIGH | introduced | internal/update/download.go:154 | A clean short chunked 206 with unknown total was accepted as complete. Declared response span must be complete; a short span retries with the partial file retained. |
| 8 | 2/1 | HIGH | touched-file | internal/bundle/resolver.go:2058 | Legacy-origin mismatch warnings still printed raw origin/effective URLs. Both fields now use the same safe URL boundary. |

## Verification

Evidence directory: Solution `.tmp/review-changes-20261012/`.

- Full affected Go race/coverage PASS for git, bundle, daemon, update, snapshot,
  downloadhttp, CLI, client and API. Coverage: 87.2%, 81.8%, 68.9%, 83.8%, 77.6%,
  83.6%, 42.8%, 35.9% and 48.4%, respectively (`go-race-phase2.log`).
- 43 frontend files / 463 tests PASS; Web ESLint PASS.
- Pinned golangci-lint 2.11.4: 0 issues.
- Forced native desktop and server builds, TypeScript and Vite PASS. A local
  Makefile overlay only redirects TypeScript/Vite caches into Solution `.tmp`;
  production source/build/link flags remain the repository's. Existing Vite
  chunk-size warning remains.
- Commons clean offline Maven verify PASS: all five selected tests, including
  discovery priority/order/tie/own-app regression coverage. No library publication.
- Real Unix HTTP staging survives a scaled response-write deadline while bytes
  arrive; missing-wrapper refusal proves Stage finished without applying anything.
- Actual UpdateDialog and API browser preview: pending apply POST, 182 status polls,
  measured progress after 181 simulated seconds, no aborted apply request. Screenshot
  `update-beyond-120s.png` was inspected; the owned browser/server were closed.
- Red-before/green-after tests cover stale cached request folding, credential URL
  diagnostics, malformed snapshot ranges, short chunked updater spans, legacy-origin
  warnings and the UI apply deadline.
- Executed read-only Go overlays neutralized six fixes independently; each
  corresponding regression test failed. A scratch Vite alias restored the 120-second
  API deadline and only its new regression failed (12 others passed). Mutation files
  never replaced repository sources.
- Existing Unix socket tests exceeded the path-length limit under the required
  Solution-local TMPDIR. An owned test overlay only shortens those socket directories;
  all tests still run, no source/GOROOT edits or omissions. The new staging fixture
  uses a short owned `os.MkdirTemp` directory and respects configured TMPDIR, so it
  also works outside this Solution layout.
- `git diff --check` PASS; both indices empty and both HEADs unchanged. No unrelated
  namespace/container/KEYCLOAK/service lifecycle operations were performed.

## Knowledge and limitations

Used full Launcher fact revision 4 with mandatory gateway revision 3, commons fact
revision 3, discovery priority-build-date fact revision 1, and applicable review
practices revisions 3. Context responses had no blockers. Knowledge was treated as
evidence, not authorization or deployed-state proof. Exact customer private-workspace
credential bindings remain unknown; no customer credentials were used in tests.

The skill's hardcoded outside state/temp locations were adapted to Solution `.tmp`.
No outside Claude state/memory/skill files were modified. New ignored Go helpers and
regressions must be preserved and explicitly included if a later commit is authorized;
local excludes were not changed. All cumulative changes remain uncommitted,
unreleased and undeployed. Pause at this completed boundary before another task.

Verified finding `p-d951fb967711e7a48bc0d341d6beedbe` is pending curation. Scoped immutable reports:
- `todo-179762aa61f2ccb4838cbc24e9b05345`: Active updater staging still has total frontend and HTTP response deadlines
- `todo-43061aa7e91fae52b2b692afdbb2c493`: Malformed snapshot partial responses append repeated bytes and reset retry counters
- `todo-b07167388eb2943396118d34c7435436`: Queued cache recovery intent leaks between independent Update and Start requests
- `todo-09c140acebfca4455553af98e43b05b1`: Git clone and legacy-origin diagnostics expose embedded URL credentials
- `todo-1157737c35036386d0ba0738b9ae4335`: Short chunked update range with unknown total is treated as a complete asset

These are observations of the inspected local baseline and corrections, not
published knowledge, TODO triage or deployed resolutions.
