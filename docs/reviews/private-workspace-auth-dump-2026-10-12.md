# Private workspace authentication dump investigation

Input: user-supplied `system-dump-20261012-124512.zip`, inspected read-only.
Archive members: system-info.json, goroutine-dump.txt, daemon-logs/daemon.log,
launcher-logs/launcher.log. No archive contents or credential values were copied.

The inspected installation is Launcher 2.16.2, commit
245cd7573ae1fdb68098c4091534cf56bef6808c, darwin/amd64, Go 1.26.9.
On 2026-10-12, logs show 29 workspace sync authentication failures and 14
bundle sync authentication failures. The private workspace repository is
`https://gitlab.citeck.ru/citeck-projects/launcher-workspaces.git`, branch core;
the bundle repository is
`https://gitlab.citeck.ru/infrastructure/docker-compose-kit.git`, branch master.
GitLab returns HTTP Basic Access denied. This message does not distinguish
expiry, invalid token, missing repository scope or an incorrect credential.

Secrets are successfully unlocked at 05:21:11 UTC, but failures continue through
05:43:58 UTC, including activation of a newly created workspace. Therefore the
initial locked vault alone does not explain the continuing failures. The archive
contains no workspace record, secret metadata, secretId binding, request payloads
or token-validation results. The exact credential selected for the failed
workspace and validity of the replacement token cannot be established from it.
This is an evidence limitation, not evidence that a token is expired.

The bundle clone separately reports a malformed IDX/packfile mismatch. Repair
reclone fails authentication and preserves the stale repository. Repair needs
working authentication; deleting caches is not an authentication fix. A historical
legacy-repository/configured-URL mismatch warning also appears, but subsequent
failures explicitly target the intended private workspace URL. It is not proven
to be the cause of the current authentication failure.

## Verified source behavior

The relevant source is unchanged from the inspected release HEAD:

- Workspace auth uses explicit WorkspaceDto.SecretID, otherwise legacy
  ws:<id>:repo when AuthType is TOKEN. Explicit links take precedence.
- A forced workspace resolve creates a fresh resolver and reads the current
  secret value; updating the linked value is not inherently stuck in a cached
  token. Secret PUT is write-only and preserves absent fields.
- Bundle auth independently uses makeTokenLookup. It overwrites the token for
  each secret type/scope as it iterates ListSecrets. SQLite lists by id, so the
  last matching id wins, irrespective of workspace binding or host. Adding a
  fresh workspace token can leave a bundle using an unrelated old GIT_TOKEN.
- GitPullErrorDialog edits/relinks the workspace secret even when the failure
  concerns a bundle repository. That action does not change the bundle fallback
  selection described above.
- Git BasicAuth username is fixed to oauth2; a stored BASIC_AUTH username is
  ignored. Personal/project/group access tokens may use this form. Deploy tokens
  require their own username/token pair; a replacement deploy token is therefore
  a separate compatibility concern, not proof of this incident's cause.
- The secret test endpoint selects a bundle URL by scope, not the workspace
  linked to the secret, and may answer no repository configured. It does not
  independently establish access to the private workspace.

A scratch Go overlay test used the real secret PUT handler and SQLite secret
service with two synthetic credentials. PUT updates the linked secret; workspace
opts resolve the fresh value; the unscoped bundle fallback still returns the old
unrelated secret. Fifteen focused daemon tests passed with the race detector,
including secret updates, workspace binding priority, migration links and force
pull behavior. No production source or user's workspace was changed for the
reproduction. Evidence: Solution `.tmp/private-ws-dump/auth-tests.log` and
`auth-repro_test.go`.

## Remaining evidence needed

To resolve the workspace-specific failure, obtain only metadata: which secretId
is selected, workspace authType, whether the replacement was edited in-place or
created as another secret, and whether the replacement is a personal/project/group
access token or deploy token. Verify read_repository and access to both repository
projects through the token owner's environment. Do not request or share the token
value. No credentials were used to contact the affected private repositories.

Primary GitLab references:
- https://docs.gitlab.com/security/tokens/access_token_scopes/
- https://docs.gitlab.com/user/project/deploy_tokens/

Knowledge context: Launcher rev4, mandatory gateway rev3; no blockers. Findings
are submitted for curation; reports are not incident resolutions.

## User clarification and verified local correction

The user clarified that the replacement is a Personal Access Token and GitLab
showed no prior usage. The latter does not establish which credential Launcher
sent: GitLab's current documentation says usage timestamps update periodically
(every ten minutes), and the affected GitLab version and exact binding are not
in the dump. A synthetic real-HTTP test confirmed that the secret PUT handler
updates the value and a forced workspace resolve sends that fresh linked value.
Normal Kotlin WorkspaceDto migration preserves repoUrl/authType; degraded
filesystem-only recovery cannot recover authType. The supplied archive cannot
establish which migration path affected this particular workspace.

The user also requested visible update errors, cache/retry/change-secret actions,
cache update time, and diagnostic logs. Local source now:

- Reports attempted workspace/bundle refresh failures despite usable cached files.
  Explicit update/start stops before runtime changes on failed refresh; startup,
  offline and config-only cached resolution remain available.
- Carries repository-specific recovery metadata in synchronous errors and
  asynchronous namespace updateGitError, including a credential reference
  captured with the actual attempted token snapshot, not guessed after mutation.
- Supports explicitly selected cached copies only within the current operation
  and its retry chain. Every URL is validated against the owning workspace and
  local data. Different-origin clones are not offered as usable cache.
- Shows the persisted successful Git sync time; missing timestamps are unknown.
- Uses PUT /api/v1/workspaces/{id}/git-secret to bind an existing GIT_TOKEN to the
  failed repository. Bundle links use the existing workspace YAML overlay with
  authType secret:<id>; the workspace repo's own token and unrelated deltas remain
  unchanged. Missing explicit links never fall back to an unrelated token.
- Logs non-secret references, availability and actual value-change flags. The
  tests explicitly assert that credential values do not appear in these logs.

The original broad legacy token fallback is retained for unbound configurations,
with ambiguity logged. An explicit repository binding is the correction path;
this is not proof that the affected user's original workspace had that state.
No credential was requested or used against the user's private GitLab projects.
No installed Launcher, commit, release or deployment was changed.

Verification evidence is under Solution `.tmp/private-ws-dump/`: real HTTP/SQLite
credential tests, repository ownership/cache tests, queue/profile isolation,
persisted-timestamp tests, frontend tests, full affected Go packages with race
coverage, pinned lint/vet, server/desktop builds, native Unix-socket discovery
smoke, and actual product-component browser screenshots. Final result receipts
are recorded in completed.md; earlier failed harness/lint runs are not passes.
