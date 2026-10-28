# Development checks

Run `make check` before committing. It covers Go race tests, coverage floors,
lint, reachable Go vulnerabilities, frontend tests and the production audit,
builds, and cross-platform compilation checks. Native macOS tests still require
a Mac; a Darwin cross-lint run does not execute them.

## Frontend dependency audit

Use the pinned pnpm version from `web/package.json`:

```sh
cd web
pnpm install --frozen-lockfile
pnpm audit --prod
pnpm audit
```

The full audit includes development tools. A clean production audit alone does
not establish that Vitest, jsdom or ESLint dependencies are free of reported
advisories. Keep both audit ignore lists empty when an upgrade is available.

The package manifest sets patched minimum versions for Vite and Vitest. Scoped
pnpm overrides also keep these development dependency paths on patched versions:

| Parent | Dependency | Minimum version |
| --- | --- | --- |
| jsdom | undici | 7.30.1 |
| minimatch | brace-expansion | 5.0.12 |
| @babel/helper-compilation-targets | browserslist | 4.28.7 |
| browserslist | baseline-browser-mapping | 2.11.0 |
| eslint-plugin-react-hooks | zod | 4.6.6 |

These overrides stay within the parents' declared compatible ranges. When
updating a parent, check whether its dependency range makes an override
redundant, regenerate the lockfile with pnpm, and run the full audit, tests,
lint and production build before removing it. Preserve unrelated lockfile
updates when bringing an older dependency branch onto master.

## Supervisor process fixtures

`TestSupervisorRestartReSelectsBinary` waits for the respawned child's marker;
fake readiness does not prove that the child has executed its script yet.

The fixture shell uses `exec sleep`, so the long-lived process is the child
owned by the supervisor. A separate `sleep` can survive termination of its shell
and hold the test runner's output pipes open: assertions print `PASS`, but
`go test` then fails with `WaitDelay expired before I/O complete`. Verify the
command's exit status, not just the test's assertion output. This lifecycle can
be checked with repeated scoped race tests as well as the full package suite.
