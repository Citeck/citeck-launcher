# Clean server acceptance verification

The user explicitly authorized removing the existing regression server and all
its test data. This phase uses a genuinely fresh Community 2026.2.2 installation
with Keycloak, self-signed HTTPS, default database content storage and no demo
snapshot. No previous namespace, secret store, cached bundle, container, Docker
volume or image existed immediately before installation.

The test-only binary is `dev-20261012-clean-server`, based on release commit
`d48cc7abc9ca47e7ce13516380b8c91543b4c77a` plus the local server-start fallback
correction. Its SHA256 is
`0683fc03601f60ada006bc33ef1ffb0f6213ba0cf20aed68152aac80df3a4d92`.
Public v2.17.0 contains the regression. The correction and regression test
are included in the subsequent user-authorized v2.17.1 hotfix. This acceptance
phase verified the test-only binary above, not a deployed v2.17.1 artifact.

## Evidence and checks

Evidence is under Solution `.tmp/clean-server-verification-20261012/`.
`remote-clean/acceptance/completed.json` records 18 passed behavioral checks:
clean cold start of all 21 applications; server discovery and unavailable APIs;
configuration validation and malformed-edit preservation; unchanged reload
planning and preservation of containers; logs and describe; missing-snapshot
refusal; manual stop/detach persistence and explicit restart; systemd daemon
restart preserving container IDs/start times; stopped snapshot export without
secrets; corrupt import refusal without auto-start; actual PostgreSQL restore
removing the post-export marker; and final healthy state.

Installer cancellation was exercised in a real terminal before configuration
creation. The interactive installer was then completed with no auto-start;
the first normal start used the native systemd service. Invalid port 70000 was
rejected in the separate installer fixture.

Mailpit received an actual synthetic SMTP message, its API returned the matching
body, and only that owned message was deleted. Server Mailpit ports remained
internal, as intended.

A browser initially observed HTTP 500 and a dashboard skeleton immediately after
service startup/restoration. A later stable check rendered the actual menu,
spaces and task widget with zero HTTP 5xx responses. A direct authenticated
model Records API read confirmed the admin person exists; no SQL bootstrap,
authentication bypass or Keycloak extension was installed. The exact cause of the initial HTTP 500 responses was not captured; their
resolution does not prove the legacy missing-admin diagnosis. Container/health
readiness alone does not prove completion of platform application initialization.

## Verification tooling and limitations

`scripts/test/server-acceptance.py` requires a configured safe SSH helper and an
exact expected Docker engine ID. Mutating lifecycle and snapshot scenarios
require explicit disposable-stand flags. It retains named results and sanitized
application states, never raw configuration, environments or credential values.
The server fallback matrix now covers ten real resolver/persisted-state cases,
including fresh bundles, absent/empty/foreign cached state and strict explicit
Git/desktop refresh behavior.

An initial isolated local Docker-in-Docker attempt used the vfs driver and
exhausted its scratch disk during cold image pulls. It was stopped and all owned
containers and Docker data were removed; that attempt is not a passing clean
platform run. The verified clean platform run uses the authorized remote native
Docker engine and overlay2. Unrelated local namespaces were preserved.

Previously verified S3 byte round-trips belong to the earlier existing-stand
phase. They are not evidence of fresh S3 installation in this DB-default phase.

## Reusing the checks

Run from the Launcher member directory. Use a credential-reference-only SSH
helper and put all output, browser profiles and caches under Solution `.tmp`.
First confirm the exact target engine and authorization for disposable mutations.

```sh
python3 scripts/test/server-acceptance.py \
  --ssh-helper <safe-helper> --expected-engine-id <verified-engine-id> \
  --output <solution-scratch-output> --exercise-disposable-server --snapshots
node scripts/test/server-ui-acceptance.mjs \
  <safe-helper> <verified-engine-id> <https-origin> <solution-scratch-output>
```

Snapshot checks require an initially empty snapshot inventory. They intentionally
retain the valid export and the database marker as regression evidence. The
browser check waits for actual dashboard initialization, then checks the
library and captures screenshots; run it after lifecycle mutations complete.
It reads the generated administrator credential in memory through the SSH
helper, and closes the browser on success or failure.

Knowledge used (full contexts, no blockers): Launcher revision 4, gateway
revision 3, server installation revision 1, server CLI revision 2,
troubleshooting revision 2, Keycloak extension revision 1, Keycloak user sync
revision 1, installation-notes revision 3 and model revision 4. The legacy
installation notes were treated as hypotheses, not live-state proof.

Final verification repeated the namespace stop/start and data-preservation path,
checked that explicit eapps restart actually changed its container ID/start time,
and preserved all container IDs/start times across a daemon restart. The final
health result is checked for `healthy: true`, not just a successful CLI exit.
All four affected packages pass with Go race detection; vet succeeds, native
server build succeeds, golangci-lint reports zero issues and the ten-case cache
matrix passes. Replaying the released synchronization intent produces the
expected failing ordinary-server cache case.

The reusable browser check passed with zero transient or final HTTP 5xx responses
on the final stable run; its dashboard and library screenshots were viewed.
The Mailpit method in the checked-in acceptance tooling also passed against the
real stand. Findings receipt `p-795c8741a24dc6b303464ab97c315919` is pending
curation; readiness observation `todo-f4993b0ed638f57ed912b401b2f1b770` remains an
open improvement report, not a verified resolution. No knowledge was published
and no report was triaged.

## DB file lifecycle and remaining platform failure

The real library form uploaded and saved a synthetic 1 MiB text file. Its
browser download and preview returned the exact original bytes, SHA256
`4e29ad18ab9f42d7c233500771a39d7c852b200baf328fd00fbbe3fecea1eb56`.
The reusable browser tool supports `--exercise-disposable-server` to create,
download, compare and delete only its uniquely named synthetic file. Its core
file lifecycle passed without HTTP 5xx.

A separate file-card check reproduced a remaining platform error after all
services were RUNNING: one `POST /gateway/api/records/query` returns HTTP 500
with `EcosWebException: App is not available: 'alfresco'`. Its requested
attributes include `_creator{id:?id,disp:?disp}`, `_created`,
`.has(n:"cm:content")` and `.att(n:"permissions"){has(n:"Write")}`.
This clean DB-only Community installation deliberately has no Alfresco service.
The exact originating widget/callsite and separately packaged ecos-ui version
have not been mapped. The observation does not prove that the Launcher changes
introduced it. No platform widget source, platform artifact, authentication
policy or optional service was changed to conceal the failure. A separately
shipped platform widget/Records correction is outside this Launcher verification
phase; the platform UI is not certified entirely green.

The verified content-access workaround is the library row Download action.
`--verify-file-card`, together with `--exercise-disposable-server`, reproduces
the additional card scenario and reports failure instead of ignoring HTTP 5xx.
The relevant browser Records API, home-page/journal and breaking-upgrade
knowledge documents were read in full at revision 1, with no blockers.
Finding `p-c3898fa5621af4e9a05d6269fd8d102c` awaits curation; scoped bug report
`todo-1bc0214c354f36c11ea9fa4fc6d07e50` remains open. No external ticket,
publication or TODO resolution was performed.

Verified deployed images are `ecos-proxy-oidc:2.27.1`, `ecos-gateway:3.7.4`,
`ecos-model:2.39.17` and `ecos-uiserv:2.36.11`. Source-only registry members
`ecos-model`, `ecos-model-lib`, `ecos-gateway` and `ecos-records` were added to
this Solution for inspection; all remained clean at the acceptance boundary. All 11 git indices were empty
at that boundary.

The final scripted file-card reproduction exits 1 for the actual HTTP 500,
reports `cardError: false`, and confirms its owned file was deleted first.
`remote-clean/ui-card-final.log` contains the explicit negative result; the
failure is not a timeout, locator error or ignored warning. Core file-lifecycle
checks and this additional card check must be reported separately.

For the additionally verified file workflow, append
`--exercise-disposable-server` to the browser command. Append
`--verify-file-card` as well to run the known failing card scenario; expect exit
1 on this inspected Community bundle until the platform compatibility issue is
corrected. The card test reports its HTTP 500 and performs owned-file deletion
before returning that negative result.
